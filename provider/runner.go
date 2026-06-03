package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow/interfaces"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

type awsRunnerConfig struct {
	cluster              string
	region               string
	subnetIDs            []string
	securityGroupIDs     []string
	taskExecutionRoleARN string
	logGroup             string
}

type awsRunnerClient interface {
	RegisterTaskDefinition(ctx context.Context, in *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	RunTask(ctx context.Context, in *ecs.RunTaskInput, optFns ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	GetLogEvents(ctx context.Context, in *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

type realAWSRunnerClient struct {
	ecs  *ecs.Client
	logs *cloudwatchlogs.Client
}

func newRealAWSRunnerClient(cfg awssdk.Config) awsRunnerClient {
	return &realAWSRunnerClient{
		ecs:  ecs.NewFromConfig(cfg),
		logs: cloudwatchlogs.NewFromConfig(cfg),
	}
}

func (c *realAWSRunnerClient) RegisterTaskDefinition(ctx context.Context, in *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	return c.ecs.RegisterTaskDefinition(ctx, in, optFns...)
}

func (c *realAWSRunnerClient) RunTask(ctx context.Context, in *ecs.RunTaskInput, optFns ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	return c.ecs.RunTask(ctx, in, optFns...)
}

func (c *realAWSRunnerClient) DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	return c.ecs.DescribeTasks(ctx, in, optFns...)
}

func (c *realAWSRunnerClient) GetLogEvents(ctx context.Context, in *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	return c.logs.GetLogEvents(ctx, in, optFns...)
}

var _ interfaces.IaCProviderRunner = (*AWSProvider)(nil)

func (p *AWSProvider) RunJob(ctx context.Context, spec interfaces.JobSpec) (*interfaces.JobHandle, error) {
	p.mu.RLock()
	client := p.runnerClient
	cfg := p.runnerConfig
	p.mu.RUnlock()
	if client == nil || cfg.cluster == "" || cfg.region == "" {
		return nil, fmt.Errorf("aws runner: provider is not initialized")
	}
	if strings.TrimSpace(spec.Image) == "" {
		return nil, fmt.Errorf("aws runner: image is required")
	}
	if strings.TrimSpace(spec.RunCommand) == "" {
		return nil, fmt.Errorf("aws runner: run_command is required")
	}

	name := awsJobName(spec.Name)
	tdOut, err := client.RegisterTaskDefinition(ctx, awsTaskDefinitionInput(name, spec, cfg))
	if err != nil {
		return nil, fmt.Errorf("aws runner: register task definition %q: %w", name, err)
	}
	taskDefARN := awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn)
	runOut, err := client.RunTask(ctx, awsRunTaskInput(name, taskDefARN, cfg))
	if err != nil {
		return nil, fmt.Errorf("aws runner: run task %q: %w", name, err)
	}
	if len(runOut.Failures) > 0 {
		return nil, fmt.Errorf("aws runner: run task %q: %s", name, awsTaskFailures(runOut.Failures))
	}
	if len(runOut.Tasks) == 0 {
		return nil, fmt.Errorf("aws runner: run task %q returned no tasks", name)
	}
	taskARN := awssdk.ToString(runOut.Tasks[0].TaskArn)
	return &interfaces.JobHandle{
		ID:       taskARN,
		Name:     name,
		Provider: "aws",
		Metadata: map[string]string{
			"cluster":             cfg.cluster,
			"task_arn":            taskARN,
			"task_definition_arn": taskDefARN,
			"container":           name,
			"log_group":           cfg.logGroup,
			"log_stream":          awsLogStream(name, taskARN),
		},
	}, nil
}

func (p *AWSProvider) JobStatus(ctx context.Context, handle interfaces.JobHandle) (*interfaces.JobStatusReply, error) {
	p.mu.RLock()
	client := p.runnerClient
	defaultCluster := p.runnerConfig.cluster
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("aws runner: provider is not initialized")
	}
	cluster := handle.Metadata["cluster"]
	if cluster == "" {
		cluster = defaultCluster
	}
	taskARN := handle.Metadata["task_arn"]
	if taskARN == "" {
		taskARN = handle.ID
	}
	if cluster == "" || taskARN == "" {
		return nil, fmt.Errorf("aws runner: cluster and task_arn metadata are required")
	}
	out, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: awssdk.String(cluster),
		Tasks:   []string{taskARN},
	})
	if err != nil {
		return nil, fmt.Errorf("aws runner: describe task %q: %w", taskARN, err)
	}
	if len(out.Tasks) == 0 {
		return nil, fmt.Errorf("aws runner: task %q not found", taskARN)
	}
	state, exitCode, message := awsTaskState(out.Tasks[0])
	return &interfaces.JobStatusReply{Handle: handle, State: state, ExitCode: exitCode, Message: message}, nil
}

func (p *AWSProvider) JobLogs(ctx context.Context, handle interfaces.JobHandle, sink interfaces.LogCaptureSink) error {
	p.mu.RLock()
	client := p.runnerClient
	p.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("aws runner: provider is not initialized")
	}
	if sink == nil {
		return nil
	}
	logGroup := handle.Metadata["log_group"]
	logStream := handle.Metadata["log_stream"]
	if logGroup == "" || logStream == "" {
		return sink.WriteLogChunk(interfaces.LogChunk{EOF: true})
	}
	out, err := client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  awssdk.String(logGroup),
		LogStreamName: awssdk.String(logStream),
		StartFromHead: awssdk.Bool(true),
		Limit:         awssdk.Int32(200),
	})
	if err != nil {
		return fmt.Errorf("aws runner: get log events %q/%q: %w", logGroup, logStream, err)
	}
	for _, event := range out.Events {
		msg := awssdk.ToString(event.Message)
		if msg == "" {
			continue
		}
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}
		if err := sink.WriteLogChunk(interfaces.LogChunk{Data: []byte(msg), Source: "stdout"}); err != nil {
			return err
		}
	}
	return sink.WriteLogChunk(interfaces.LogChunk{EOF: true})
}

func awsTaskDefinitionInput(name string, spec interfaces.JobSpec, cfg awsRunnerConfig) *ecs.RegisterTaskDefinitionInput {
	launchCompat := ecstypes.CompatibilityEc2
	networkMode := ecstypes.NetworkModeBridge
	if len(cfg.subnetIDs) > 0 {
		launchCompat = ecstypes.CompatibilityFargate
		networkMode = ecstypes.NetworkModeAwsvpc
	}
	in := &ecs.RegisterTaskDefinitionInput{
		Family:                  awssdk.String(name),
		RequiresCompatibilities: []ecstypes.Compatibility{launchCompat},
		NetworkMode:             networkMode,
		Cpu:                     awssdk.String("256"),
		Memory:                  awssdk.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:        awssdk.String(name),
			Image:       awssdk.String(spec.Image),
			Essential:   awssdk.Bool(true),
			EntryPoint:  []string{"/bin/sh", "-c"},
			Command:     []string{spec.RunCommand},
			Environment: awsJobEnvironment(spec.EnvVars),
			Secrets:     awsJobSecrets(spec.EnvVarsSecret),
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         cfg.logGroup,
					"awslogs-region":        cfg.region,
					"awslogs-stream-prefix": name,
					"awslogs-create-group":  "true",
				},
			},
		}},
	}
	if cfg.taskExecutionRoleARN != "" {
		in.ExecutionRoleArn = awssdk.String(cfg.taskExecutionRoleARN)
	}
	return in
}

func awsRunTaskInput(name, taskDefARN string, cfg awsRunnerConfig) *ecs.RunTaskInput {
	in := &ecs.RunTaskInput{
		Cluster:        awssdk.String(cfg.cluster),
		TaskDefinition: awssdk.String(taskDefARN),
		StartedBy:      awssdk.String(awsStartedBy(name)),
		LaunchType:     ecstypes.LaunchTypeEc2,
	}
	if len(cfg.subnetIDs) > 0 {
		in.LaunchType = ecstypes.LaunchTypeFargate
		in.NetworkConfiguration = &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        append([]string(nil), cfg.subnetIDs...),
				SecurityGroups: append([]string(nil), cfg.securityGroupIDs...),
				AssignPublicIp: ecstypes.AssignPublicIpEnabled,
			},
		}
	}
	return in
}

func awsJobEnvironment(values map[string]string) []ecstypes.KeyValuePair {
	keys := sortedAWSMapKeys(values)
	out := make([]ecstypes.KeyValuePair, 0, len(keys))
	for _, key := range keys {
		out = append(out, ecstypes.KeyValuePair{Name: awssdk.String(key), Value: awssdk.String(values[key])})
	}
	return out
}

func awsJobSecrets(values map[string]string) []ecstypes.Secret {
	keys := sortedAWSMapKeys(values)
	out := make([]ecstypes.Secret, 0, len(keys))
	for _, key := range keys {
		out = append(out, ecstypes.Secret{Name: awssdk.String(key), ValueFrom: awssdk.String(values[key])})
	}
	return out
}

func sortedAWSMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var nonAWSJobName = regexp.MustCompile(`[^a-z0-9-]+`)

func awsJobName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = nonAWSJobName.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "provider-ephemeral-job"
	}
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())
	maxBase := 64 - len(suffix)
	if len(name) > maxBase {
		name = strings.TrimRight(name[:maxBase], "-")
	}
	return name + suffix
}

func awsStartedBy(name string) string {
	if len(name) <= 36 {
		return name
	}
	return name[:36]
}

func awsLogStream(containerName, taskARN string) string {
	taskID := taskARN
	if i := strings.LastIndex(taskID, "/"); i >= 0 && i < len(taskID)-1 {
		taskID = taskID[i+1:]
	}
	return containerName + "/" + containerName + "/" + taskID
}

func awsTaskFailures(failures []ecstypes.Failure) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		reason := awssdk.ToString(failure.Reason)
		arn := awssdk.ToString(failure.Arn)
		if arn != "" && reason != "" {
			parts = append(parts, arn+": "+reason)
		} else if reason != "" {
			parts = append(parts, reason)
		} else if arn != "" {
			parts = append(parts, arn)
		}
	}
	return strings.Join(parts, "; ")
}

func awsTaskState(task ecstypes.Task) (interfaces.JobState, int, string) {
	status := strings.ToUpper(awssdk.ToString(task.LastStatus))
	exitCode := 0
	message := awssdk.ToString(task.StoppedReason)
	for _, container := range task.Containers {
		if container.ExitCode != nil {
			exitCode = int(*container.ExitCode)
		}
		if message == "" {
			message = awssdk.ToString(container.Reason)
		}
	}
	switch status {
	case "PROVISIONING", "PENDING", "ACTIVATING":
		return interfaces.JobStatePending, exitCode, message
	case "RUNNING":
		return interfaces.JobStateRunning, exitCode, message
	case "STOPPED", "DEACTIVATING", "DEPROVISIONING":
		if exitCode == 0 {
			return interfaces.JobStateSucceeded, exitCode, message
		}
		return interfaces.JobStateFailed, exitCode, message
	default:
		return interfaces.JobStateUnknown, exitCode, message
	}
}

func awsLogMessages(events []logtypes.OutputLogEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		if msg := awssdk.ToString(event.Message); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

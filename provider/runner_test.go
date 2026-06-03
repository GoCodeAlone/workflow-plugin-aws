package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

type fakeAWSRunnerClient struct {
	registerIn *ecs.RegisterTaskDefinitionInput
	runIn      *ecs.RunTaskInput
	describeIn *ecs.DescribeTasksInput
	logsIn     *cloudwatchlogs.GetLogEventsInput
	task       ecstypes.Task
	events     []logtypes.OutputLogEvent
}

func (f *fakeAWSRunnerClient) RegisterTaskDefinition(_ context.Context, in *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	f.registerIn = in
	return &ecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{
		TaskDefinitionArn: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/" + awssdk.ToString(in.Family) + ":1"),
	}}, nil
}

func (f *fakeAWSRunnerClient) RunTask(_ context.Context, in *ecs.RunTaskInput, _ ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	f.runIn = in
	return &ecs.RunTaskOutput{Tasks: []ecstypes.Task{{
		TaskArn: awssdk.String("arn:aws:ecs:us-east-1:123:cluster/default/task/task-1"),
	}}}, nil
}

func (f *fakeAWSRunnerClient) DescribeTasks(_ context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	f.describeIn = in
	return &ecs.DescribeTasksOutput{Tasks: []ecstypes.Task{f.task}}, nil
}

func (f *fakeAWSRunnerClient) GetLogEvents(_ context.Context, in *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	f.logsIn = in
	return &cloudwatchlogs.GetLogEventsOutput{Events: f.events}, nil
}

func TestAWSRunnerRunJobCreatesFargateTask(t *testing.T) {
	client := &fakeAWSRunnerClient{}
	p := &AWSProvider{
		initialized:  true,
		runnerClient: client,
		runnerConfig: awsRunnerConfig{
			cluster:              "default",
			region:               "us-east-1",
			subnetIDs:            []string{"subnet-1"},
			securityGroupIDs:     []string{"sg-1"},
			taskExecutionRoleARN: "arn:aws:iam::123:role/ecsTaskExecutionRole",
			logGroup:             "/workflow/provider-ephemeral",
		},
	}

	handle, err := p.RunJob(context.Background(), interfaces.JobSpec{
		Name:          "Migrate DB!",
		Image:         "123.dkr.ecr.us-east-1.amazonaws.com/app:migrate",
		RunCommand:    "bin/migrate up",
		EnvVars:       map[string]string{"PLAIN": "value"},
		EnvVarsSecret: map[string]string{"DATABASE_URL": "arn:aws:ssm:us-east-1:123:parameter/db"},
	})
	if err != nil {
		t.Fatalf("RunJob returned error: %v", err)
	}
	if handle.Provider != "aws" || handle.Metadata["task_arn"] == "" || handle.Metadata["log_stream"] == "" {
		t.Fatalf("handle = %+v", handle)
	}
	if client.registerIn.NetworkMode != ecstypes.NetworkModeAwsvpc ||
		client.registerIn.RequiresCompatibilities[0] != ecstypes.CompatibilityFargate {
		t.Fatalf("task def network/compat = %s/%v", client.registerIn.NetworkMode, client.registerIn.RequiresCompatibilities)
	}
	container := client.registerIn.ContainerDefinitions[0]
	if awssdk.ToString(container.Image) != "123.dkr.ecr.us-east-1.amazonaws.com/app:migrate" {
		t.Fatalf("image = %q", awssdk.ToString(container.Image))
	}
	if len(container.EntryPoint) != 2 || container.EntryPoint[0] != "/bin/sh" || container.EntryPoint[1] != "-c" ||
		len(container.Command) != 1 || container.Command[0] != "bin/migrate up" {
		t.Fatalf("entrypoint=%v command=%v", container.EntryPoint, container.Command)
	}
	if !hasAWSEnv(container.Environment, "PLAIN", "value") {
		t.Fatalf("missing env: %#v", container.Environment)
	}
	if !hasAWSSecret(container.Secrets, "DATABASE_URL", "arn:aws:ssm:us-east-1:123:parameter/db") {
		t.Fatalf("missing secret: %#v", container.Secrets)
	}
	if client.runIn.LaunchType != ecstypes.LaunchTypeFargate ||
		client.runIn.NetworkConfiguration == nil ||
		len(client.runIn.NetworkConfiguration.AwsvpcConfiguration.Subnets) != 1 {
		t.Fatalf("run task input = %#v", client.runIn)
	}
}

func TestAWSRunnerStatusAndLogs(t *testing.T) {
	exit := int32(7)
	client := &fakeAWSRunnerClient{
		task: ecstypes.Task{
			LastStatus:    awssdk.String("STOPPED"),
			StoppedReason: awssdk.String("Essential container exited"),
			Containers: []ecstypes.Container{{
				ExitCode: &exit,
			}},
		},
		events: []logtypes.OutputLogEvent{{Message: awssdk.String("migration failed")}},
	}
	p := &AWSProvider{runnerClient: client, runnerConfig: awsRunnerConfig{cluster: "default"}}
	handle := interfaces.JobHandle{
		ID: "task-1",
		Metadata: map[string]string{
			"cluster":    "default",
			"task_arn":   "task-1",
			"log_group":  "/workflow/provider-ephemeral",
			"log_stream": "job/job/task-1",
		},
	}

	status, err := p.JobStatus(context.Background(), handle)
	if err != nil {
		t.Fatalf("JobStatus returned error: %v", err)
	}
	if status.State != interfaces.JobStateFailed || status.ExitCode != 7 {
		t.Fatalf("status = %+v", status)
	}

	sink := &runnerSink{}
	if err := p.JobLogs(context.Background(), handle, sink); err != nil {
		t.Fatalf("JobLogs returned error: %v", err)
	}
	if string(sink.data) != "migration failed\n" || !sink.eof {
		t.Fatalf("sink data=%q eof=%v", string(sink.data), sink.eof)
	}
}

func hasAWSEnv(values []ecstypes.KeyValuePair, key, value string) bool {
	for _, env := range values {
		if awssdk.ToString(env.Name) == key && awssdk.ToString(env.Value) == value {
			return true
		}
	}
	return false
}

func hasAWSSecret(values []ecstypes.Secret, key, value string) bool {
	for _, secret := range values {
		if awssdk.ToString(secret.Name) == key && awssdk.ToString(secret.ValueFrom) == value {
			return true
		}
	}
	return false
}

type runnerSink struct {
	data []byte
	eof  bool
}

func (s *runnerSink) WriteLogChunk(chunk interfaces.LogChunk) error {
	if chunk.EOF {
		s.eof = true
		return nil
	}
	if strings.Contains(strings.ToLower(chunk.Source), "stderr") {
		return nil
	}
	s.data = append(s.data, chunk.Data...)
	return nil
}

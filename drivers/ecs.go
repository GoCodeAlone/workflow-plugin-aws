package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// ECSClient is the subset of ECS API used by ECSDriver.
type ECSClient interface {
	RegisterTaskDefinition(ctx context.Context, params *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	CreateService(ctx context.Context, params *ecs.CreateServiceInput, optFns ...func(*ecs.Options)) (*ecs.CreateServiceOutput, error)
	DescribeServices(ctx context.Context, params *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
	UpdateService(ctx context.Context, params *ecs.UpdateServiceInput, optFns ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error)
	DeleteService(ctx context.Context, params *ecs.DeleteServiceInput, optFns ...func(*ecs.Options)) (*ecs.DeleteServiceOutput, error)
	DeregisterTaskDefinition(ctx context.Context, params *ecs.DeregisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error)
}

// ECSDriver manages ECS Fargate services (infra.container_service).
type ECSDriver struct {
	client  ECSClient
	cluster string
}

// NewECSDriver creates an ECS driver from an AWS config.
func NewECSDriver(cfg awssdk.Config, cluster string) *ECSDriver {
	if cluster == "" {
		cluster = "default"
	}
	return &ECSDriver{client: ecs.NewFromConfig(cfg), cluster: cluster}
}

// NewECSDriverWithClient creates an ECS driver with a custom client (for tests).
func NewECSDriverWithClient(client ECSClient, cluster string) *ECSDriver {
	if cluster == "" {
		cluster = "default"
	}
	return &ECSDriver{client: client, cluster: cluster}
}

func (d *ECSDriver) ResourceType() string { return "infra.container_service" }

func (d *ECSDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	image, _ := spec.Config["image"].(string)
	if image == "" {
		return nil, fmt.Errorf("ecs: create %q: image is required", spec.Name)
	}
	cpu, _ := spec.Config["cpu"].(string)
	if cpu == "" {
		cpu = "256"
	}
	memory, _ := spec.Config["memory"].(string)
	if memory == "" {
		memory = "512"
	}
	replicas := int32(intProp(spec.Config, "replicas", 1))

	// Register task definition
	tdIn := &ecs.RegisterTaskDefinitionInput{
		Family:                  awssdk.String(spec.Name),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		Cpu:                     awssdk.String(cpu),
		Memory:                  awssdk.String(memory),
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{
				Name:  awssdk.String(spec.Name),
				Image: awssdk.String(image),
				Essential: awssdk.Bool(true),
			},
		},
	}
	tdOut, err := d.client.RegisterTaskDefinition(ctx, tdIn)
	if err != nil {
		return nil, fmt.Errorf("ecs: register task def %q: %w", spec.Name, err)
	}
	tdARN := awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn)

	subnetIDs := stringSliceProp(spec.Config, "subnet_ids")
	sgIDs := stringSliceProp(spec.Config, "security_group_ids")

	svcIn := &ecs.CreateServiceInput{
		Cluster:        awssdk.String(d.cluster),
		ServiceName:    awssdk.String(spec.Name),
		TaskDefinition: awssdk.String(tdARN),
		DesiredCount:   awssdk.Int32(replicas),
		LaunchType:     ecstypes.LaunchTypeFargate,
	}
	if len(subnetIDs) > 0 || len(sgIDs) > 0 {
		svcIn.NetworkConfiguration = &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        subnetIDs,
				SecurityGroups: sgIDs,
				AssignPublicIp: ecstypes.AssignPublicIpEnabled,
			},
		}
	}

	svcOut, err := d.client.CreateService(ctx, svcIn)
	if err != nil {
		return nil, fmt.Errorf("ecs: create service %q: %w", spec.Name, err)
	}

	return ecsServiceToOutput(spec.Name, svcOut.Service), nil
}

func (d *ECSDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(d.cluster),
		Services: []string{ref.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("ecs: describe service %q: %w", ref.Name, err)
	}
	if len(out.Services) == 0 {
		return nil, fmt.Errorf("ecs: service %q not found", ref.Name)
	}
	return ecsServiceToOutput(ref.Name, &out.Services[0]), nil
}

func (d *ECSDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	in := &ecs.UpdateServiceInput{
		Cluster: awssdk.String(d.cluster),
		Service: awssdk.String(ref.Name),
	}
	if _, ok := spec.Config["replicas"]; ok {
		in.DesiredCount = awssdk.Int32(int32(intProp(spec.Config, "replicas", 1)))
	}
	if image, _ := spec.Config["image"].(string); image != "" {
		// Re-register task definition with new image
		cpu, _ := spec.Config["cpu"].(string)
		if cpu == "" {
			cpu = "256"
		}
		mem, _ := spec.Config["memory"].(string)
		if mem == "" {
			mem = "512"
		}
		tdOut, err := d.client.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
			Family:                  awssdk.String(ref.Name),
			RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
			NetworkMode:             ecstypes.NetworkModeAwsvpc,
			Cpu:                     awssdk.String(cpu),
			Memory:                  awssdk.String(mem),
			ContainerDefinitions: []ecstypes.ContainerDefinition{
				{Name: awssdk.String(ref.Name), Image: awssdk.String(image), Essential: awssdk.Bool(true)},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("ecs: re-register task def %q: %w", ref.Name, err)
		}
		in.TaskDefinition = awssdk.String(awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn))
	}

	out, err := d.client.UpdateService(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("ecs: update service %q: %w", ref.Name, err)
	}
	return ecsServiceToOutput(ref.Name, out.Service), nil
}

func (d *ECSDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	// Scale to 0 first
	_, _ = d.client.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      awssdk.String(d.cluster),
		Service:      awssdk.String(ref.Name),
		DesiredCount: awssdk.Int32(0),
	})
	_, err := d.client.DeleteService(ctx, &ecs.DeleteServiceInput{
		Cluster: awssdk.String(d.cluster),
		Service: awssdk.String(ref.Name),
		Force:   awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("ecs: delete service %q: %w", ref.Name, err)
	}
	return nil
}

func (d *ECSDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *ECSDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	healthy := out.Status == "running"
	msg := out.Status
	if !healthy {
		msg = fmt.Sprintf("service status: %s", out.Status)
	}
	return &interfaces.HealthResult{Healthy: healthy, Message: msg}, nil
}

func (d *ECSDriver) Scale(ctx context.Context, ref interfaces.ResourceRef, replicas int) (*interfaces.ResourceOutput, error) {
	out, err := d.client.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      awssdk.String(d.cluster),
		Service:      awssdk.String(ref.Name),
		DesiredCount: awssdk.Int32(int32(replicas)),
	})
	if err != nil {
		return nil, fmt.Errorf("ecs: scale service %q: %w", ref.Name, err)
	}
	return ecsServiceToOutput(ref.Name, out.Service), nil
}

func ecsServiceToOutput(name string, svc *ecstypes.Service) *interfaces.ResourceOutput {
	if svc == nil {
		return &interfaces.ResourceOutput{Name: name, Type: "infra.container_service", Status: "unknown"}
	}
	status := "running"
	svcStatus := awssdk.ToString(svc.Status)
	switch svcStatus {
	case "INACTIVE":
		status = "stopped"
	case "DRAINING":
		status = "degraded"
	}

	outputs := map[string]any{
		"status":          svcStatus,
		"desired_count":   svc.DesiredCount,
		"running_count":   svc.RunningCount,
		"task_definition": awssdk.ToString(svc.TaskDefinition),
	}
	if svc.ServiceArn != nil {
		outputs["arn"] = *svc.ServiceArn
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.container_service",
		ProviderID: awssdk.ToString(svc.ServiceArn),
		Outputs:    outputs,
		Status:     status,
	}
}

var _ interfaces.ResourceDriver = (*ECSDriver)(nil)

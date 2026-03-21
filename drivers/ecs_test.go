package drivers_test

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockECSClient struct {
	registerOut  *ecs.RegisterTaskDefinitionOutput
	registerErr  error
	createSvcOut *ecs.CreateServiceOutput
	createSvcErr error
	describeOut  *ecs.DescribeServicesOutput
	describeErr  error
	updateOut    *ecs.UpdateServiceOutput
	updateErr    error
	deleteOut    *ecs.DeleteServiceOutput
	deleteErr    error
	deregErr     error
}

func (m *mockECSClient) RegisterTaskDefinition(_ context.Context, _ *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	return m.registerOut, m.registerErr
}
func (m *mockECSClient) CreateService(_ context.Context, _ *ecs.CreateServiceInput, _ ...func(*ecs.Options)) (*ecs.CreateServiceOutput, error) {
	return m.createSvcOut, m.createSvcErr
}
func (m *mockECSClient) DescribeServices(_ context.Context, _ *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockECSClient) UpdateService(_ context.Context, _ *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	return m.updateOut, m.updateErr
}
func (m *mockECSClient) DeleteService(_ context.Context, _ *ecs.DeleteServiceInput, _ ...func(*ecs.Options)) (*ecs.DeleteServiceOutput, error) {
	return m.deleteOut, m.deleteErr
}
func (m *mockECSClient) DeregisterTaskDefinition(_ context.Context, _ *ecs.DeregisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error) {
	return &ecs.DeregisterTaskDefinitionOutput{}, m.deregErr
}

func TestECSDriver_Create(t *testing.T) {
	svcARN := "arn:aws:ecs:us-east-1:123:service/default/my-svc"
	mock := &mockECSClient{
		registerOut: &ecs.RegisterTaskDefinitionOutput{
			TaskDefinition: &ecstypes.TaskDefinition{
				TaskDefinitionArn: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/my-svc:1"),
			},
		},
		createSvcOut: &ecs.CreateServiceOutput{
			Service: &ecstypes.Service{
				ServiceArn:      awssdk.String(svcARN),
				ServiceName:     awssdk.String("my-svc"),
				Status:          awssdk.String("ACTIVE"),
				DesiredCount:    1,
				RunningCount:    0,
				TaskDefinition:  awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/my-svc:1"),
			},
		},
	}
	d := drivers.NewECSDriverWithClient(mock, "default")
	spec := interfaces.ResourceSpec{
		Name: "my-svc",
		Type: "infra.container_service",
		Config: map[string]any{
			"image":    "nginx:latest",
			"cpu":      "256",
			"memory":   "512",
			"replicas": 1,
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.container_service" {
		t.Errorf("expected type infra.container_service, got %s", out.Type)
	}
	if out.ProviderID != svcARN {
		t.Errorf("expected ProviderID %s, got %s", svcARN, out.ProviderID)
	}
}

func TestECSDriver_Create_MissingImage(t *testing.T) {
	d := drivers.NewECSDriverWithClient(&mockECSClient{}, "default")
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "svc",
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestECSDriver_Read(t *testing.T) {
	mock := &mockECSClient{
		describeOut: &ecs.DescribeServicesOutput{
			Services: []ecstypes.Service{
				{
					ServiceArn:     awssdk.String("arn:aws:ecs:us-east-1:123:service/default/my-svc"),
					ServiceName:    awssdk.String("my-svc"),
					Status:         awssdk.String("ACTIVE"),
					DesiredCount:   1,
					RunningCount:   1,
					TaskDefinition: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/my-svc:1"),
				},
			},
		},
	}
	d := drivers.NewECSDriverWithClient(mock, "default")
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-svc"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-svc" {
		t.Errorf("expected my-svc, got %s", out.Name)
	}
}

func TestECSDriver_Scale(t *testing.T) {
	mock := &mockECSClient{
		updateOut: &ecs.UpdateServiceOutput{
			Service: &ecstypes.Service{
				ServiceArn:     awssdk.String("arn:..."),
				ServiceName:    awssdk.String("my-svc"),
				Status:         awssdk.String("ACTIVE"),
				DesiredCount:   3,
				RunningCount:   1,
				TaskDefinition: awssdk.String("td:1"),
			},
		},
	}
	d := drivers.NewECSDriverWithClient(mock, "default")
	out, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-svc"}, 3)
	if err != nil {
		t.Fatalf("Scale failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestECSDriver_Delete(t *testing.T) {
	mock := &mockECSClient{
		updateOut: &ecs.UpdateServiceOutput{
			Service: &ecstypes.Service{
				ServiceArn:     awssdk.String("arn:..."),
				ServiceName:    awssdk.String("my-svc"),
				Status:         awssdk.String("ACTIVE"),
				DesiredCount:   0,
				TaskDefinition: awssdk.String("td:1"),
			},
		},
		deleteOut: &ecs.DeleteServiceOutput{},
	}
	d := drivers.NewECSDriverWithClient(mock, "default")
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-svc"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
)

// ─── Mock clients ─────────────────────────────────────────────────────────────

type mockDeployECS struct {
	registerOut    *ecs.RegisterTaskDefinitionOutput
	registerErr    error
	descSvcOut     *ecs.DescribeServicesOutput
	descSvcErr     error
	updateSvcOut   *ecs.UpdateServiceOutput
	updateSvcErr   error
	createSvcOut   *ecs.CreateServiceOutput
	createSvcErr   error
	deleteSvcOut   *ecs.DeleteServiceOutput
	deleteSvcErr   error
	descTDOut      *ecs.DescribeTaskDefinitionOutput
	descTDErr      error
	// Track calls for rollback verification.
	updateCalls    []string
	registerCalls  []string
}

func (m *mockDeployECS) RegisterTaskDefinition(_ context.Context, in *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	m.registerCalls = append(m.registerCalls, awssdk.ToString(in.Family))
	return m.registerOut, m.registerErr
}
func (m *mockDeployECS) CreateService(_ context.Context, _ *ecs.CreateServiceInput, _ ...func(*ecs.Options)) (*ecs.CreateServiceOutput, error) {
	return m.createSvcOut, m.createSvcErr
}
func (m *mockDeployECS) DescribeServices(_ context.Context, in *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return m.descSvcOut, m.descSvcErr
}
func (m *mockDeployECS) UpdateService(_ context.Context, in *ecs.UpdateServiceInput, _ ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	m.updateCalls = append(m.updateCalls, awssdk.ToString(in.Service))
	return m.updateSvcOut, m.updateSvcErr
}
func (m *mockDeployECS) DeleteService(_ context.Context, _ *ecs.DeleteServiceInput, _ ...func(*ecs.Options)) (*ecs.DeleteServiceOutput, error) {
	return m.deleteSvcOut, m.deleteSvcErr
}
func (m *mockDeployECS) DeregisterTaskDefinition(_ context.Context, _ *ecs.DeregisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error) {
	return &ecs.DeregisterTaskDefinitionOutput{}, nil
}
func (m *mockDeployECS) DescribeTaskDefinition(_ context.Context, _ *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	return m.descTDOut, m.descTDErr
}

type mockDeployELB struct {
	createTGOut   *elbv2.CreateTargetGroupOutput
	createTGErr   error
	deleteTGErr   error
	modifyErr     error
	modifyCalls   int
}

func (m *mockDeployELB) CreateTargetGroup(_ context.Context, _ *elbv2.CreateTargetGroupInput, _ ...func(*elbv2.Options)) (*elbv2.CreateTargetGroupOutput, error) {
	return m.createTGOut, m.createTGErr
}
func (m *mockDeployELB) DeleteTargetGroup(_ context.Context, _ *elbv2.DeleteTargetGroupInput, _ ...func(*elbv2.Options)) (*elbv2.DeleteTargetGroupOutput, error) {
	return &elbv2.DeleteTargetGroupOutput{}, m.deleteTGErr
}
func (m *mockDeployELB) ModifyListener(_ context.Context, _ *elbv2.ModifyListenerInput, _ ...func(*elbv2.Options)) (*elbv2.ModifyListenerOutput, error) {
	m.modifyCalls++
	return &elbv2.ModifyListenerOutput{}, m.modifyErr
}

type mockDeployCW struct {
	out *cloudwatch.GetMetricStatisticsOutput
	err error
}

func (m *mockDeployCW) GetMetricStatistics(_ context.Context, _ *cloudwatch.GetMetricStatisticsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error) {
	return m.out, m.err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func defaultECSMock() *mockDeployECS {
	return &mockDeployECS{
		registerOut: &ecs.RegisterTaskDefinitionOutput{
			TaskDefinition: &ecstypes.TaskDefinition{
				TaskDefinitionArn: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/svc:2"),
			},
		},
		descSvcOut: &ecs.DescribeServicesOutput{
			Services: []ecstypes.Service{
				{
					ServiceArn:     awssdk.String("arn:aws:ecs:us-east-1:123:service/default/svc"),
					ServiceName:    awssdk.String("svc"),
					Status:         awssdk.String("ACTIVE"),
					DesiredCount:   1,
					RunningCount:   1,
					TaskDefinition: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/svc:1"),
				},
			},
		},
		updateSvcOut: &ecs.UpdateServiceOutput{
			Service: &ecstypes.Service{
				ServiceArn:     awssdk.String("arn:aws:ecs:us-east-1:123:service/default/svc"),
				ServiceName:    awssdk.String("svc"),
				Status:         awssdk.String("ACTIVE"),
				DesiredCount:   1,
				RunningCount:   1,
				TaskDefinition: awssdk.String("arn:aws:ecs:us-east-1:123:task-definition/svc:2"),
			},
		},
		createSvcOut: &ecs.CreateServiceOutput{
			Service: &ecstypes.Service{
				ServiceArn:  awssdk.String("arn:aws:ecs:us-east-1:123:service/default/svc-green"),
				ServiceName: awssdk.String("svc-green"),
				Status:      awssdk.String("ACTIVE"),
			},
		},
		deleteSvcOut: &ecs.DeleteServiceOutput{},
		descTDOut: &ecs.DescribeTaskDefinitionOutput{
			TaskDefinition: &ecstypes.TaskDefinition{
				ContainerDefinitions: []ecstypes.ContainerDefinition{
					{Image: awssdk.String("nginx:1.24")},
				},
			},
		},
	}
}

func defaultELBMock() *mockDeployELB {
	return &mockDeployELB{
		createTGOut: &elbv2.CreateTargetGroupOutput{
			TargetGroups: []elbtypes.TargetGroup{
				{TargetGroupArn: awssdk.String("arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/svc-green-tg/abc123")},
			},
		},
	}
}

func defaultConfig() drivers.ECSDeployConfig {
	return drivers.ECSDeployConfig{
		Cluster:         "default",
		ServiceName:     "svc",
		ListenerARN:     "arn:aws:elasticloadbalancing:us-east-1:123:listener/app/my-alb/abc/def",
		StableTGARN:     "arn:aws:elasticloadbalancing:us-east-1:123:targetgroup/svc-tg/111",
		ALBDNS:          "my-alb-123456789.us-east-1.elb.amazonaws.com",
		MetricThreshold: 0.05,
	}
}

func newTestDriver(ecsMock *mockDeployECS, elbMock *mockDeployELB, cwMock *mockDeployCW) *drivers.ECSDeployDriver {
	return drivers.NewECSDeployDriverWithClients(ecsMock, elbMock, cwMock, defaultConfig())
}

// ─── DeployDriver tests ───────────────────────────────────────────────────────

func TestDeployDriver_Update_HappyPath(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	if err := d.Update(context.Background(), "nginx:1.25"); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
}

func TestDeployDriver_Update_RegisterError_Rollback(t *testing.T) {
	ecsMock := defaultECSMock()
	ecsMock.registerErr = fmt.Errorf("register failed")
	d := newTestDriver(ecsMock, defaultELBMock(), &mockDeployCW{})
	if err := d.Update(context.Background(), "nginx:1.25"); err == nil {
		t.Fatal("expected error on register failure")
	}
}

func TestDeployDriver_Update_UpdateServiceError(t *testing.T) {
	ecsMock := defaultECSMock()
	ecsMock.updateSvcErr = fmt.Errorf("update service failed")
	d := newTestDriver(ecsMock, defaultELBMock(), &mockDeployCW{})
	if err := d.Update(context.Background(), "nginx:1.25"); err == nil {
		t.Fatal("expected error on update service failure")
	}
}

func TestDeployDriver_HealthCheck_Healthy(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	if err := d.HealthCheck(context.Background(), "/health"); err != nil {
		t.Fatalf("HealthCheck failed unexpectedly: %v", err)
	}
}

func TestDeployDriver_HealthCheck_Unhealthy(t *testing.T) {
	ecsMock := defaultECSMock()
	ecsMock.descSvcOut.Services[0].RunningCount = 0
	ecsMock.descSvcOut.Services[0].DesiredCount = 1
	d := newTestDriver(ecsMock, defaultELBMock(), &mockDeployCW{})
	if err := d.HealthCheck(context.Background(), "/health"); err == nil {
		t.Fatal("expected error when running < desired")
	}
}

func TestDeployDriver_HealthCheck_FailedRollout(t *testing.T) {
	ecsMock := defaultECSMock()
	ecsMock.descSvcOut.Services[0].RunningCount = 1
	ecsMock.descSvcOut.Services[0].DesiredCount = 1
	ecsMock.descSvcOut.Services[0].Deployments = []ecstypes.Deployment{
		{RolloutState: ecstypes.DeploymentRolloutStateFailed},
	}
	d := newTestDriver(ecsMock, defaultELBMock(), &mockDeployCW{})
	if err := d.HealthCheck(context.Background(), "/health"); err == nil {
		t.Fatal("expected error for FAILED rollout state")
	}
}

func TestDeployDriver_CurrentImage(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	img, err := d.CurrentImage(context.Background())
	if err != nil {
		t.Fatalf("CurrentImage failed: %v", err)
	}
	if img != "nginx:1.24" {
		t.Errorf("expected nginx:1.24, got %s", img)
	}
}

func TestDeployDriver_ReplicaCount(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	count, err := d.ReplicaCount(context.Background())
	if err != nil {
		t.Fatalf("ReplicaCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

// ─── BlueGreenDriver tests ────────────────────────────────────────────────────

func TestBlueGreenDriver_FullLifecycle(t *testing.T) {
	ecsMock := defaultECSMock()
	elbMock := defaultELBMock()
	d := newTestDriver(ecsMock, elbMock, &mockDeployCW{})

	// CreateGreen
	if err := d.CreateGreen(context.Background(), "nginx:1.25"); err != nil {
		t.Fatalf("CreateGreen failed: %v", err)
	}

	// HealthCheck (should now target green service)
	if err := d.HealthCheck(context.Background(), "/health"); err != nil {
		t.Fatalf("HealthCheck after CreateGreen failed: %v", err)
	}

	// GreenEndpoint
	ep, err := d.GreenEndpoint(context.Background())
	if err != nil {
		t.Fatalf("GreenEndpoint failed: %v", err)
	}
	if ep == "" {
		t.Error("expected non-empty green endpoint")
	}

	// SwitchTraffic
	if err := d.SwitchTraffic(context.Background()); err != nil {
		t.Fatalf("SwitchTraffic failed: %v", err)
	}
	if elbMock.modifyCalls != 1 {
		t.Errorf("expected 1 ModifyListener call, got %d", elbMock.modifyCalls)
	}

	// DestroyBlue
	if err := d.DestroyBlue(context.Background()); err != nil {
		t.Fatalf("DestroyBlue failed: %v", err)
	}
}

func TestBlueGreenDriver_CreateGreen_ECSError(t *testing.T) {
	ecsMock := defaultECSMock()
	ecsMock.createSvcErr = fmt.Errorf("insufficient capacity")
	d := newTestDriver(ecsMock, defaultELBMock(), &mockDeployCW{})
	if err := d.CreateGreen(context.Background(), "nginx:1.25"); err == nil {
		t.Fatal("expected error on CreateService failure")
	}
}

func TestBlueGreenDriver_SwitchTraffic_NoGreen(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	if err := d.SwitchTraffic(context.Background()); err == nil {
		t.Fatal("expected error when no green target group exists")
	}
}

func TestBlueGreenDriver_GreenEndpoint_NoDNS(t *testing.T) {
	cfg := defaultConfig()
	cfg.ALBDNS = ""
	d := drivers.NewECSDeployDriverWithClients(defaultECSMock(), defaultELBMock(), &mockDeployCW{}, cfg)
	if _, err := d.GreenEndpoint(context.Background()); err == nil {
		t.Fatal("expected error when ALB DNS not configured")
	}
}

// ─── CanaryDriver tests ───────────────────────────────────────────────────────

func TestCanaryDriver_FullLifecycle_GatePasses(t *testing.T) {
	cwMock := &mockDeployCW{
		out: &cloudwatch.GetMetricStatisticsOutput{
			Datapoints: []cwtypes.Datapoint{
				{Average: awssdk.Float64(0.01)}, // below 0.05 threshold
			},
		},
	}
	d := newTestDriver(defaultECSMock(), defaultELBMock(), cwMock)

	if err := d.CreateCanary(context.Background(), "nginx:1.25"); err != nil {
		t.Fatalf("CreateCanary failed: %v", err)
	}
	if err := d.RoutePercent(context.Background(), 10); err != nil {
		t.Fatalf("RoutePercent failed: %v", err)
	}
	if err := d.CheckMetricGate(context.Background(), "error_rate"); err != nil {
		t.Fatalf("CheckMetricGate should pass: %v", err)
	}
	if err := d.PromoteCanary(context.Background()); err != nil {
		t.Fatalf("PromoteCanary failed: %v", err)
	}
}

func TestCanaryDriver_FullLifecycle_GateFails(t *testing.T) {
	cwMock := &mockDeployCW{
		out: &cloudwatch.GetMetricStatisticsOutput{
			Datapoints: []cwtypes.Datapoint{
				{Average: awssdk.Float64(0.12)}, // above 0.05 threshold
			},
		},
	}
	elbMock := defaultELBMock()
	d := newTestDriver(defaultECSMock(), elbMock, cwMock)

	if err := d.CreateCanary(context.Background(), "nginx:1.25"); err != nil {
		t.Fatalf("CreateCanary failed: %v", err)
	}
	if err := d.RoutePercent(context.Background(), 10); err != nil {
		t.Fatalf("RoutePercent failed: %v", err)
	}
	if err := d.CheckMetricGate(context.Background(), "error_rate"); err == nil {
		t.Fatal("expected metric gate to fail")
	}

	// Canary should be destroyed on rollback.
	if err := d.DestroyCanary(context.Background()); err != nil {
		t.Fatalf("DestroyCanary failed: %v", err)
	}
}

func TestCanaryDriver_CheckMetricGate_NoData(t *testing.T) {
	cwMock := &mockDeployCW{
		out: &cloudwatch.GetMetricStatisticsOutput{
			Datapoints: []cwtypes.Datapoint{},
		},
	}
	d := newTestDriver(defaultECSMock(), defaultELBMock(), cwMock)
	// No data → gate should pass.
	if err := d.CheckMetricGate(context.Background(), "error_rate"); err != nil {
		t.Fatalf("expected gate to pass with no datapoints: %v", err)
	}
}

func TestCanaryDriver_CheckMetricGate_CloudWatchError(t *testing.T) {
	cwMock := &mockDeployCW{err: fmt.Errorf("cloudwatch unavailable")}
	d := newTestDriver(defaultECSMock(), defaultELBMock(), cwMock)
	if err := d.CheckMetricGate(context.Background(), "error_rate"); err == nil {
		t.Fatal("expected error on CloudWatch failure")
	}
}

func TestCanaryDriver_RoutePercent_NoCanary(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	if err := d.RoutePercent(context.Background(), 20); err == nil {
		t.Fatal("expected error when no canary target group exists")
	}
}

func TestCanaryDriver_DestroyCanary_NopIfNotCreated(t *testing.T) {
	d := newTestDriver(defaultECSMock(), defaultELBMock(), &mockDeployCW{})
	// Should be a no-op with no error.
	if err := d.DestroyCanary(context.Background()); err != nil {
		t.Fatalf("DestroyCanary no-op failed: %v", err)
	}
}

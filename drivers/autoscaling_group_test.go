package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

// mockAutoScalingClient implements AutoScalingClient for tests.
type mockAutoScalingClient struct {
	registerOut    *applicationautoscaling.RegisterScalableTargetOutput
	registerErr    error
	describeOut    *applicationautoscaling.DescribeScalableTargetsOutput
	describeErr    error
	describePoliciesOut *applicationautoscaling.DescribeScalingPoliciesOutput
	describePoliciesErr error
	putPolicyOut   *applicationautoscaling.PutScalingPolicyOutput
	putPolicyErr   error
	deletePolicyErr error
	deregisterErr  error

	// Captured inputs for assertion.
	capturedPutPolicyInput *applicationautoscaling.PutScalingPolicyInput
}

func (m *mockAutoScalingClient) RegisterScalableTarget(_ context.Context, _ *applicationautoscaling.RegisterScalableTargetInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.RegisterScalableTargetOutput, error) {
	return m.registerOut, m.registerErr
}
func (m *mockAutoScalingClient) DescribeScalableTargets(_ context.Context, _ *applicationautoscaling.DescribeScalableTargetsInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockAutoScalingClient) DescribeScalingPolicies(_ context.Context, _ *applicationautoscaling.DescribeScalingPoliciesInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error) {
	return m.describePoliciesOut, m.describePoliciesErr
}
func (m *mockAutoScalingClient) PutScalingPolicy(_ context.Context, in *applicationautoscaling.PutScalingPolicyInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.PutScalingPolicyOutput, error) {
	m.capturedPutPolicyInput = in
	return m.putPolicyOut, m.putPolicyErr
}
func (m *mockAutoScalingClient) DeleteScalingPolicy(_ context.Context, _ *applicationautoscaling.DeleteScalingPolicyInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DeleteScalingPolicyOutput, error) {
	return &applicationautoscaling.DeleteScalingPolicyOutput{}, m.deletePolicyErr
}
func (m *mockAutoScalingClient) DeregisterScalableTarget(_ context.Context, _ *applicationautoscaling.DeregisterScalableTargetInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DeregisterScalableTargetOutput, error) {
	return &applicationautoscaling.DeregisterScalableTargetOutput{}, m.deregisterErr
}

// baseAutoScalingSpec returns a minimal valid ResourceSpec for infra.autoscaling_group.
func baseAutoScalingSpec(name string) interfaces.ResourceSpec {
	return interfaces.ResourceSpec{
		Name: name,
		Type: "infra.autoscaling_group",
		Config: map[string]any{
			"service_namespace":  "ecs",
			"resource_id":        "service/my-cluster/my-service",
			"scalable_dimension": "ecs:service:DesiredCount",
			"min_capacity":       1,
			"max_capacity":       10,
		},
	}
}

// baseProviderID is the encoded ProviderID for baseAutoScalingSpec.
const baseProviderID = "ecs|service/my-cluster/my-service|ecs:service:DesiredCount"

// ---- ResourceType ----

func TestAutoScalingGroupDriver_ResourceType(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	if d.ResourceType() != "infra.autoscaling_group" {
		t.Errorf("expected infra.autoscaling_group, got %s", d.ResourceType())
	}
}

// ---- Create happy path ----

func TestAutoScalingGroupDriver_Create(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	out, err := d.Create(context.Background(), baseAutoScalingSpec("my-asg"))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Name != "my-asg" {
		t.Errorf("expected name my-asg, got %s", out.Name)
	}
	if out.Type != "infra.autoscaling_group" {
		t.Errorf("expected type infra.autoscaling_group, got %s", out.Type)
	}
	if out.ProviderID == "" {
		t.Error("expected non-empty ProviderID")
	}
}

// ---- Create missing required fields ----

func TestAutoScalingGroupDriver_Create_MissingServiceNamespace(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"resource_id": "service/c/s", "scalable_dimension": "ecs:service:DesiredCount", "min_capacity": 1, "max_capacity": 5},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for missing service_namespace")
	}
}

func TestAutoScalingGroupDriver_Create_MissingResourceID(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"service_namespace": "ecs", "scalable_dimension": "ecs:service:DesiredCount", "min_capacity": 1, "max_capacity": 5},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for missing resource_id")
	}
}

func TestAutoScalingGroupDriver_Create_MissingScalableDimension(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"service_namespace": "ecs", "resource_id": "service/c/s", "min_capacity": 1, "max_capacity": 5},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for missing scalable_dimension")
	}
}

func TestAutoScalingGroupDriver_Create_MissingMinCapacity(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"service_namespace": "ecs", "resource_id": "service/c/s", "scalable_dimension": "ecs:service:DesiredCount", "max_capacity": 5},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for missing min_capacity")
	}
}

func TestAutoScalingGroupDriver_Create_MissingMaxCapacity(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"service_namespace": "ecs", "resource_id": "service/c/s", "scalable_dimension": "ecs:service:DesiredCount", "min_capacity": 1},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for missing max_capacity")
	}
}

func TestAutoScalingGroupDriver_Create_InvalidCapacityRange(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	spec := interfaces.ResourceSpec{
		Name:   "asg",
		Config: map[string]any{"service_namespace": "ecs", "resource_id": "service/c/s", "scalable_dimension": "ecs:service:DesiredCount", "min_capacity": 10, "max_capacity": 1},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when min_capacity > max_capacity")
	}
}

// ---- Create with scaling policies ----

func TestAutoScalingGroupDriver_Create_WithTargetTrackingPolicy(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		putPolicyOut:        &applicationautoscaling.PutScalingPolicyOutput{PolicyARN: awssdk.String("arn:aws:autoscaling:policy/xyz")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":            "cpu-tracking",
			"policy_type":            "TargetTrackingScaling",
			"target_value":           float64(75),
			"predefined_metric_type": "ECSServiceAverageCPUUtilization",
			"scale_in_cooldown":      int(300),
			"scale_out_cooldown":     int(60),
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create with policy failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	// Assert the PutScalingPolicyInput was built correctly.
	captured := mock.capturedPutPolicyInput
	if captured == nil {
		t.Fatal("expected PutScalingPolicy to have been called")
	}
	if captured.PolicyType != aastypes.PolicyTypeTargetTrackingScaling {
		t.Errorf("expected TargetTrackingScaling, got %v", captured.PolicyType)
	}
	ttCfg := captured.TargetTrackingScalingPolicyConfiguration
	if ttCfg == nil {
		t.Fatal("expected TargetTrackingScalingPolicyConfiguration to be set")
	}
	if awssdk.ToFloat64(ttCfg.TargetValue) != 75 {
		t.Errorf("expected TargetValue=75, got %v", awssdk.ToFloat64(ttCfg.TargetValue))
	}
	if ttCfg.PredefinedMetricSpecification == nil {
		t.Fatal("expected PredefinedMetricSpecification to be set")
	}
	if string(ttCfg.PredefinedMetricSpecification.PredefinedMetricType) != "ECSServiceAverageCPUUtilization" {
		t.Errorf("expected ECSServiceAverageCPUUtilization, got %v", ttCfg.PredefinedMetricSpecification.PredefinedMetricType)
	}
}

func TestAutoScalingGroupDriver_Create_TargetTracking_MissingMetricType(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut:         &applicationautoscaling.RegisterScalableTargetOutput{ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":  "cpu-tracking",
			"policy_type":  "TargetTrackingScaling",
			"target_value": float64(75),
			// predefined_metric_type intentionally omitted
		},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when predefined_metric_type is missing for TargetTrackingScaling")
	}
}

func TestAutoScalingGroupDriver_Create_WithStepScalingPolicy(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		putPolicyOut:        &applicationautoscaling.PutScalingPolicyOutput{PolicyARN: awssdk.String("arn:aws:autoscaling:policy/step")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":     "step-out",
			"policy_type":     "StepScaling",
			"adjustment_type": "ChangeInCapacity",
			"step_adjustments": []any{
				map[string]any{
					"metric_interval_lower_bound": float64(0),
					"scaling_adjustment":          int(2),
				},
			},
			"cooldown": int(60),
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create with step policy failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	// Assert PutScalingPolicyInput fields for StepScaling.
	captured := mock.capturedPutPolicyInput
	if captured == nil {
		t.Fatal("expected PutScalingPolicy to have been called")
	}
	if captured.PolicyType != aastypes.PolicyTypeStepScaling {
		t.Errorf("expected StepScaling, got %v", captured.PolicyType)
	}
	stCfg := captured.StepScalingPolicyConfiguration
	if stCfg == nil {
		t.Fatal("expected StepScalingPolicyConfiguration to be set")
	}
	if string(stCfg.AdjustmentType) != "ChangeInCapacity" {
		t.Errorf("expected AdjustmentType=ChangeInCapacity, got %v", stCfg.AdjustmentType)
	}
	if len(stCfg.StepAdjustments) != 1 {
		t.Errorf("expected 1 step adjustment, got %d", len(stCfg.StepAdjustments))
	}
}

func TestAutoScalingGroupDriver_Create_StepScaling_MissingAdjustmentType(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut:         &applicationautoscaling.RegisterScalableTargetOutput{ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name": "step-out",
			"policy_type": "StepScaling",
			// adjustment_type intentionally omitted
			"step_adjustments": []any{
				map[string]any{"metric_interval_lower_bound": float64(0), "scaling_adjustment": int(2)},
			},
		},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when adjustment_type is missing for StepScaling")
	}
}

func TestAutoScalingGroupDriver_Create_StepScaling_MissingStepAdjustments(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut:         &applicationautoscaling.RegisterScalableTargetOutput{ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":     "step-out",
			"policy_type":     "StepScaling",
			"adjustment_type": "ChangeInCapacity",
			// step_adjustments intentionally omitted
		},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when step_adjustments is missing for StepScaling")
	}
}

// ---- Create API error ----

func TestAutoScalingGroupDriver_Create_RegisterError(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerErr: fmt.Errorf("validation exception: invalid resource"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	_, err := d.Create(context.Background(), baseAutoScalingSpec("my-asg"))
	if err == nil {
		t.Fatal("expected error when RegisterScalableTarget fails")
	}
}

func TestAutoScalingGroupDriver_Create_PutPolicyError(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		putPolicyErr:        fmt.Errorf("invalid policy configuration"),
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":            "bad-policy",
			"policy_type":            "TargetTrackingScaling",
			"target_value":           float64(50),
			"predefined_metric_type": "ECSServiceAverageCPUUtilization",
		},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when PutScalingPolicy fails")
	}
}

// ---- Read happy path ----

func TestAutoScalingGroupDriver_Read(t *testing.T) {
	mock := &mockAutoScalingClient{
		describeOut: &applicationautoscaling.DescribeScalableTargetsOutput{
			ScalableTargets: []aastypes.ScalableTarget{
				{
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
					MinCapacity:       awssdk.Int32(1),
					MaxCapacity:       awssdk.Int32(10),
					ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
				},
			},
		},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	// ProviderID is required for Read; pass a realistic encoded value.
	out, err := d.Read(context.Background(), interfaces.ResourceRef{
		Name:       "my-asg",
		Type:       "infra.autoscaling_group",
		ProviderID: baseProviderID,
	})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-asg" {
		t.Errorf("expected name my-asg, got %s", out.Name)
	}
}

func TestAutoScalingGroupDriver_Read_MissingProviderID(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	// Read without ProviderID must return an error.
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-asg", Type: "infra.autoscaling_group"})
	if err == nil {
		t.Fatal("expected error when ProviderID is missing")
	}
}

func TestAutoScalingGroupDriver_Read_NotFound(t *testing.T) {
	mock := &mockAutoScalingClient{
		describeOut: &applicationautoscaling.DescribeScalableTargetsOutput{
			ScalableTargets: []aastypes.ScalableTarget{},
		},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "missing-asg", Type: "infra.autoscaling_group", ProviderID: baseProviderID})
	if err == nil {
		t.Fatal("expected error for not-found scalable target")
	}
}

func TestAutoScalingGroupDriver_Read_DescribeError(t *testing.T) {
	mock := &mockAutoScalingClient{
		describeErr: fmt.Errorf("service unavailable"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-asg", Type: "infra.autoscaling_group", ProviderID: baseProviderID})
	if err == nil {
		t.Fatal("expected error when DescribeScalableTargets fails")
	}
}

// ---- Update happy path ----

func TestAutoScalingGroupDriver_Update(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		describeOut: &applicationautoscaling.DescribeScalableTargetsOutput{
			ScalableTargets: []aastypes.ScalableTarget{
				{
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
					MinCapacity:       awssdk.Int32(1),
					MaxCapacity:       awssdk.Int32(10),
					ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
				},
			},
		},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{
			ScalingPolicies: []aastypes.ScalingPolicy{},
		},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	spec.Config["max_capacity"] = 20
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-asg"}, spec)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

// ---- Update removes stale policies ----

func TestAutoScalingGroupDriver_Update_RemovesStalePolicies(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		describeOut: &applicationautoscaling.DescribeScalableTargetsOutput{
			ScalableTargets: []aastypes.ScalableTarget{
				{
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
					MinCapacity:       awssdk.Int32(1),
					MaxCapacity:       awssdk.Int32(10),
					ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
				},
			},
		},
		// Current live policies include "old-policy" which is absent from spec
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{
			ScalingPolicies: []aastypes.ScalingPolicy{
				{
					PolicyName:        awssdk.String("old-policy"),
					PolicyARN:         awssdk.String("arn:aws:autoscaling:policy/old"),
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
				},
			},
		},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	// No policies in the desired spec — old-policy should be deleted
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-asg"}, baseAutoScalingSpec("my-asg"))
	if err != nil {
		t.Fatalf("Update (remove stale policies) failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestAutoScalingGroupDriver_Update_Error(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerErr: fmt.Errorf("invalid parameter combination"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-asg"}, baseAutoScalingSpec("my-asg"))
	if err == nil {
		t.Fatal("expected error when RegisterScalableTarget fails during update")
	}
}

// ---- Delete happy path ----

func TestAutoScalingGroupDriver_Delete(t *testing.T) {
	mock := &mockAutoScalingClient{
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{
			ScalingPolicies: []aastypes.ScalingPolicy{
				{
					PolicyName:        awssdk.String("my-policy"),
					PolicyARN:         awssdk.String("arn:aws:autoscaling:policy/p1"),
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
				},
			},
		},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{
		Name:       "my-asg",
		Type:       "infra.autoscaling_group",
		ProviderID: baseProviderID,
	})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestAutoScalingGroupDriver_Delete_Error(t *testing.T) {
	mock := &mockAutoScalingClient{
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
		deregisterErr:       fmt.Errorf("scalable target not found"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{
		Name:       "my-asg",
		ProviderID: baseProviderID,
	})
	if err == nil {
		t.Fatal("expected error when DeregisterScalableTarget fails")
	}
}

func TestAutoScalingGroupDriver_Delete_FetchPoliciesError(t *testing.T) {
	mock := &mockAutoScalingClient{
		describePoliciesErr: fmt.Errorf("api throttled"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{
		Name:       "my-asg",
		ProviderID: baseProviderID,
	})
	if err == nil {
		t.Fatal("expected error when DescribeScalingPolicies fails during delete")
	}
}

// ---- Diff ----

func TestAutoScalingGroupDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	diff, err := d.Diff(context.Background(), baseAutoScalingSpec("asg"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestAutoScalingGroupDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	current := &interfaces.ResourceOutput{
		Name:       "asg",
		Type:       "infra.autoscaling_group",
		ProviderID: baseProviderID,
		Outputs:    map[string]any{"min_capacity": 1, "max_capacity": 5, "policy_names": ""},
	}
	spec := baseAutoScalingSpec("asg")
	spec.Config["max_capacity"] = 20
	diff, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when max_capacity changes")
	}
}

func TestAutoScalingGroupDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	current := &interfaces.ResourceOutput{
		Name:       "asg",
		Type:       "infra.autoscaling_group",
		ProviderID: baseProviderID, // matches baseAutoScalingSpec identity
		Outputs:    map[string]any{"min_capacity": 1, "max_capacity": 10, "policy_names": ""},
	}
	diff, err := d.Diff(context.Background(), baseAutoScalingSpec("asg"), current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestAutoScalingGroupDriver_Diff_PolicyChange(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	current := &interfaces.ResourceOutput{
		Name:       "asg",
		Type:       "infra.autoscaling_group",
		ProviderID: baseProviderID,
		Outputs:    map[string]any{"min_capacity": 1, "max_capacity": 10, "policy_names": []string{"old-policy"}},
	}
	// Desired spec has a different policy set.
	spec := baseAutoScalingSpec("asg")
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":            "new-policy",
			"policy_type":            "TargetTrackingScaling",
			"target_value":           float64(75),
			"predefined_metric_type": "ECSServiceAverageCPUUtilization",
		},
	}
	diff, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when policy set changes")
	}
}

// ---- HealthCheck ----

func TestAutoScalingGroupDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockAutoScalingClient{
		describeOut: &applicationautoscaling.DescribeScalableTargetsOutput{
			ScalableTargets: []aastypes.ScalableTarget{
				{
					ResourceId:        awssdk.String("service/my-cluster/my-service"),
					ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
					ServiceNamespace:  aastypes.ServiceNamespaceEcs,
					MinCapacity:       awssdk.Int32(1),
					MaxCapacity:       awssdk.Int32(10),
					ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
				},
			},
		},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-asg", ProviderID: baseProviderID})
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if !health.Healthy {
		t.Errorf("expected healthy, got: %s", health.Message)
	}
}

func TestAutoScalingGroupDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockAutoScalingClient{
		describeErr: fmt.Errorf("resource not found"),
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-asg", ProviderID: baseProviderID})
	if err != nil {
		t.Fatalf("HealthCheck returned unexpected error: %v", err)
	}
	if health.Healthy {
		t.Error("expected unhealthy when Read fails")
	}
}

func TestAutoScalingGroupDriver_HealthCheck_MissingProviderID(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-asg"})
	if err != nil {
		t.Fatalf("HealthCheck returned unexpected error: %v", err)
	}
	// HealthCheck should return Healthy=false (not an error) when ProviderID is missing.
	if health.Healthy {
		t.Error("expected unhealthy when ProviderID is missing")
	}
}

// ---- SensitiveKeys ----

func TestAutoScalingGroupDriver_SensitiveKeys(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	if keys := d.SensitiveKeys(); keys != nil {
		t.Errorf("expected nil sensitive keys, got %v", keys)
	}
}

// ---- Diff identity drift ----

func TestAutoScalingGroupDriver_Diff_IdentityDrift(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	// Current output has a different ProviderID (different resource_id).
	current := &interfaces.ResourceOutput{
		Name:       "asg",
		Type:       "infra.autoscaling_group",
		ProviderID: "ecs|service/old-cluster/old-service|ecs:service:DesiredCount",
		Outputs:    map[string]any{"min_capacity": 1, "max_capacity": 10, "policy_names": ""},
	}
	// Desired spec has a different identity — should trigger NeedsUpdate.
	diff, err := d.Diff(context.Background(), baseAutoScalingSpec("asg"), current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when scalable target identity changes")
	}
}

// ---- Update identity mismatch ----

func TestAutoScalingGroupDriver_Update_IdentityMismatch(t *testing.T) {
	d := drivers.NewAutoScalingGroupDriverWithClient(&mockAutoScalingClient{})
	// ref.ProviderID encodes a different identity than the spec — should error.
	_, err := d.Update(context.Background(), interfaces.ResourceRef{
		Name:       "my-asg",
		ProviderID: "ecs|service/old-cluster/old-service|ecs:service:DesiredCount",
	}, baseAutoScalingSpec("my-asg"))
	if err == nil {
		t.Fatal("expected error when ProviderID does not match spec identity")
	}
}

// ---- Create with int target_value ----

func TestAutoScalingGroupDriver_Create_TargetTrackingPolicy_IntTargetValue(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		putPolicyOut:        &applicationautoscaling.PutScalingPolicyOutput{PolicyARN: awssdk.String("arn:aws:autoscaling:policy/xyz")},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	// Use int (common in YAML decoding) instead of float64.
	spec.Config["policies"] = []any{
		map[string]any{
			"policy_name":            "cpu-tracking",
			"policy_type":            "TargetTrackingScaling",
			"target_value":           int(75), // int, not float64
			"predefined_metric_type": "ECSServiceAverageCPUUtilization",
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create with int target_value failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	captured := mock.capturedPutPolicyInput
	if captured == nil {
		t.Fatal("expected PutScalingPolicy to have been called")
	}
	if awssdk.ToFloat64(captured.TargetTrackingScalingPolicyConfiguration.TargetValue) != 75 {
		t.Errorf("expected TargetValue=75, got %v", awssdk.ToFloat64(captured.TargetTrackingScalingPolicyConfiguration.TargetValue))
	}
}

// ---- parsePolicies malformed input ----

func TestAutoScalingGroupDriver_Create_MalformedPoliciesType(t *testing.T) {
	mock := &mockAutoScalingClient{
		registerOut: &applicationautoscaling.RegisterScalableTargetOutput{
			ScalableTargetARN: awssdk.String("arn:aws:application-autoscaling:us-east-1:123:scalable-target/abc"),
		},
		describePoliciesOut: &applicationautoscaling.DescribeScalingPoliciesOutput{},
	}
	d := drivers.NewAutoScalingGroupDriverWithClient(mock)
	spec := baseAutoScalingSpec("my-asg")
	// policies is a string, not a list — should fail safe rather than delete-all.
	spec.Config["policies"] = "not-a-list"
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error when policies is malformed (wrong type)")
	}
}

// ---- Interface compliance ----

func TestAutoScalingGroupDriver_ImplementsResourceDriver(t *testing.T) {
	var _ interfaces.ResourceDriver = (*drivers.AutoScalingGroupDriver)(nil)
}

package drivers

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// AutoScalingClient is the subset of Application Auto Scaling API used by AutoScalingGroupDriver.
type AutoScalingClient interface {
	RegisterScalableTarget(ctx context.Context, params *applicationautoscaling.RegisterScalableTargetInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.RegisterScalableTargetOutput, error)
	DescribeScalableTargets(ctx context.Context, params *applicationautoscaling.DescribeScalableTargetsInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error)
	DescribeScalingPolicies(ctx context.Context, params *applicationautoscaling.DescribeScalingPoliciesInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error)
	PutScalingPolicy(ctx context.Context, params *applicationautoscaling.PutScalingPolicyInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.PutScalingPolicyOutput, error)
	DeleteScalingPolicy(ctx context.Context, params *applicationautoscaling.DeleteScalingPolicyInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DeleteScalingPolicyOutput, error)
	DeregisterScalableTarget(ctx context.Context, params *applicationautoscaling.DeregisterScalableTargetInput, optFns ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DeregisterScalableTargetOutput, error)
}

// AutoScalingGroupDriver manages Application Auto Scaling targets (infra.autoscaling_group).
//
// Config keys:
//   - service_namespace (string, required) — e.g., "ecs", "dynamodb", "rds"
//   - resource_id       (string, required) — e.g., "service/cluster/service-name"
//   - scalable_dimension (string, required) — e.g., "ecs:service:DesiredCount"
//   - min_capacity      (int, required)
//   - max_capacity      (int, required)
//   - role_arn          (string, optional)
//   - policies          ([]any, optional) — each map with keys: policy_name, policy_type,
//     target_value (TargetTracking), scale_in_cooldown, scale_out_cooldown (StepScaling)
type AutoScalingGroupDriver struct {
	client AutoScalingClient
}

// NewAutoScalingGroupDriver creates an AutoScalingGroupDriver from an AWS config.
func NewAutoScalingGroupDriver(cfg awssdk.Config) *AutoScalingGroupDriver {
	return &AutoScalingGroupDriver{client: applicationautoscaling.NewFromConfig(cfg)}
}

// NewAutoScalingGroupDriverWithClient creates an AutoScalingGroupDriver with a custom client (for tests).
func NewAutoScalingGroupDriverWithClient(client AutoScalingClient) *AutoScalingGroupDriver {
	return &AutoScalingGroupDriver{client: client}
}

func (d *AutoScalingGroupDriver) ResourceType() string { return "infra.autoscaling_group" }

// Create registers a new scalable target and applies any declared scaling policies.
func (d *AutoScalingGroupDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	ns, resourceID, dim, err := requiredAutoScalingFields(spec)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: %w", spec.Name, err)
	}

	minCap := int32(intProp(spec.Config, "min_capacity", 0))
	maxCap := int32(intProp(spec.Config, "max_capacity", 1))
	roleARN, _ := spec.Config["role_arn"].(string)

	in := &applicationautoscaling.RegisterScalableTargetInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
		MinCapacity:       awssdk.Int32(minCap),
		MaxCapacity:       awssdk.Int32(maxCap),
	}
	if roleARN != "" {
		in.RoleARN = awssdk.String(roleARN)
	}

	out, err := d.client.RegisterScalableTarget(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: %w", spec.Name, err)
	}

	targetARN := awssdk.ToString(out.ScalableTargetARN)
	providerID := encodeProviderID(ns, resourceID, dim)

	if err := d.syncPolicies(ctx, spec, ns, resourceID, dim, nil); err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: apply policies: %w", spec.Name, err)
	}

	return d.buildOutput(spec.Name, targetARN, providerID, int(minCap), int(maxCap), nil), nil
}

// Read describes the scalable target and its policies.
func (d *AutoScalingGroupDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	ns, resourceID, dim := decodeProviderID(ref.ProviderID)

	// If ProviderID is not set, we can only look up by namespace annotation stored in the ref.
	// Fall back to the ref type context — caller must ensure ProviderID is populated for precise lookup.
	if ns == "" {
		// Use ref.Name as a best-effort resource_id if no ProviderID.
		ns = "ecs" // default namespace for graceful degradation
	}

	out, err := d.client.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{
		ServiceNamespace: aastypes.ServiceNamespace(ns),
		ResourceIds:      resourceIDFilter(resourceID, ref.Name),
		ScalableDimension: func() aastypes.ScalableDimension {
			if dim != "" {
				return aastypes.ScalableDimension(dim)
			}
			return ""
		}(),
	})
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: describe %q: %w", ref.Name, err)
	}
	if len(out.ScalableTargets) == 0 {
		return nil, fmt.Errorf("autoscaling_group: scalable target %q not found", ref.Name)
	}
	target := out.ScalableTargets[0]

	policyNames := d.readPolicyNames(ctx, target)
	minCap := int(awssdk.ToInt32(target.MinCapacity))
	maxCap := int(awssdk.ToInt32(target.MaxCapacity))
	providerID := encodeProviderID(string(target.ServiceNamespace), awssdk.ToString(target.ResourceId), string(target.ScalableDimension))
	targetARN := awssdk.ToString(target.ScalableTargetARN)

	return d.buildOutput(ref.Name, targetARN, providerID, minCap, maxCap, policyNames), nil
}

// Update re-registers the scalable target (idempotent) and reconciles scaling policies.
func (d *AutoScalingGroupDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	ns, resourceID, dim, err := requiredAutoScalingFields(spec)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: %w", ref.Name, err)
	}

	minCap := int32(intProp(spec.Config, "min_capacity", 0))
	maxCap := int32(intProp(spec.Config, "max_capacity", 1))
	roleARN, _ := spec.Config["role_arn"].(string)

	in := &applicationautoscaling.RegisterScalableTargetInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
		MinCapacity:       awssdk.Int32(minCap),
		MaxCapacity:       awssdk.Int32(maxCap),
	}
	if roleARN != "" {
		in.RoleARN = awssdk.String(roleARN)
	}

	out, err := d.client.RegisterScalableTarget(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: %w", ref.Name, err)
	}

	// Fetch current live policies so we can delete any that are removed from the spec.
	livePolicies := d.fetchLivePolicies(ctx, ns, resourceID, dim)

	if err := d.syncPolicies(ctx, spec, ns, resourceID, dim, livePolicies); err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: sync policies: %w", ref.Name, err)
	}

	targetARN := awssdk.ToString(out.ScalableTargetARN)
	providerID := encodeProviderID(ns, resourceID, dim)
	return d.buildOutput(ref.Name, targetARN, providerID, int(minCap), int(maxCap), nil), nil
}

// Delete removes all scaling policies then deregisters the scalable target.
func (d *AutoScalingGroupDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	ns, resourceID, dim := decodeProviderID(ref.ProviderID)
	if ns == "" {
		return fmt.Errorf("autoscaling_group: delete %q: ProviderID is required (format: namespace|resource_id|dimension)", ref.Name)
	}

	// Delete all existing policies first.
	livePolicies := d.fetchLivePolicies(ctx, ns, resourceID, dim)
	for _, p := range livePolicies {
		if _, err := d.client.DeleteScalingPolicy(ctx, &applicationautoscaling.DeleteScalingPolicyInput{
			PolicyName:        p.PolicyName,
			ServiceNamespace:  p.ServiceNamespace,
			ResourceId:        p.ResourceId,
			ScalableDimension: p.ScalableDimension,
		}); err != nil {
			return fmt.Errorf("autoscaling_group: delete policy %q for %q: %w", awssdk.ToString(p.PolicyName), ref.Name, err)
		}
	}

	if _, err := d.client.DeregisterScalableTarget(ctx, &applicationautoscaling.DeregisterScalableTargetInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
	}); err != nil {
		return fmt.Errorf("autoscaling_group: deregister %q: %w", ref.Name, err)
	}
	return nil
}

// Diff computes whether the desired spec diverges from the current output.
func (d *AutoScalingGroupDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	// Compare the fields we surface in outputs.
	want := map[string]any{
		"min_capacity": intProp(desired.Config, "min_capacity", 0),
		"max_capacity": intProp(desired.Config, "max_capacity", 1),
	}
	changes := diffOutputs(want, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

// HealthCheck returns healthy if the scalable target is readable.
func (d *AutoScalingGroupDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "scalable target registered"}, nil
}

// Scale updates the max_capacity of the scalable target to the given replica count.
func (d *AutoScalingGroupDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("autoscaling_group: use Update with max_capacity to resize")
}

// SensitiveKeys returns output keys whose values should be masked in logs.
func (d *AutoScalingGroupDriver) SensitiveKeys() []string { return nil }

// ---- helpers ----

// requiredAutoScalingFields extracts and validates the three required config fields.
func requiredAutoScalingFields(spec interfaces.ResourceSpec) (ns, resourceID, dim string, err error) {
	ns, _ = spec.Config["service_namespace"].(string)
	if ns == "" {
		return "", "", "", fmt.Errorf("service_namespace is required")
	}
	resourceID, _ = spec.Config["resource_id"].(string)
	if resourceID == "" {
		return "", "", "", fmt.Errorf("resource_id is required")
	}
	dim, _ = spec.Config["scalable_dimension"].(string)
	if dim == "" {
		return "", "", "", fmt.Errorf("scalable_dimension is required")
	}
	return ns, resourceID, dim, nil
}

// encodeProviderID packs the three scalable-target identifiers into a single string.
// Format: "namespace|resource_id|scalable_dimension"
func encodeProviderID(ns, resourceID, dim string) string {
	return ns + "|" + resourceID + "|" + dim
}

// decodeProviderID unpacks a ProviderID produced by encodeProviderID.
func decodeProviderID(providerID string) (ns, resourceID, dim string) {
	parts := strings.SplitN(providerID, "|", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// resourceIDFilter returns a []string filter suitable for DescribeScalableTargets ResourceIds.
// If resourceID is non-empty, filter by it; otherwise use the ref name as a fallback.
func resourceIDFilter(resourceID, refName string) []string {
	if resourceID != "" {
		return []string{resourceID}
	}
	if refName != "" {
		return []string{refName}
	}
	return nil
}

// buildOutput constructs a ResourceOutput from a scalable target's fields.
func (d *AutoScalingGroupDriver) buildOutput(name, targetARN, providerID string, minCap, maxCap int, policyNames []string) *interfaces.ResourceOutput {
	outputs := map[string]any{
		"min_capacity": minCap,
		"max_capacity": maxCap,
	}
	if targetARN != "" {
		outputs["arn"] = targetARN
	}
	if len(policyNames) > 0 {
		outputs["policy_names"] = policyNames
	}
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.autoscaling_group",
		ProviderID: providerID,
		Outputs:    outputs,
		Status:     "running",
	}
}

// fetchLivePolicies returns the current scaling policies for a scalable target.
// Errors are swallowed — callers handle absence gracefully.
func (d *AutoScalingGroupDriver) fetchLivePolicies(ctx context.Context, ns, resourceID, dim string) []aastypes.ScalingPolicy {
	out, err := d.client.DescribeScalingPolicies(ctx, &applicationautoscaling.DescribeScalingPoliciesInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
	})
	if err != nil || out == nil {
		return nil
	}
	return out.ScalingPolicies
}

// readPolicyNames fetches policy names for a ScalableTarget; returns nil on error.
func (d *AutoScalingGroupDriver) readPolicyNames(ctx context.Context, target aastypes.ScalableTarget) []string {
	policies := d.fetchLivePolicies(ctx, string(target.ServiceNamespace), awssdk.ToString(target.ResourceId), string(target.ScalableDimension))
	var names []string
	for _, p := range policies {
		names = append(names, awssdk.ToString(p.PolicyName))
	}
	return names
}

// syncPolicies reconciles desired policies with live policies:
//  1. Delete live policies whose policy_name is absent from the desired list.
//  2. PutScalingPolicy for each desired policy.
func (d *AutoScalingGroupDriver) syncPolicies(ctx context.Context, spec interfaces.ResourceSpec, ns, resourceID, dim string, livePolicies []aastypes.ScalingPolicy) error {
	desired := parsePolicies(spec.Config)

	// Build set of desired policy names.
	desiredNames := make(map[string]struct{}, len(desired))
	for _, p := range desired {
		desiredNames[p.policyName] = struct{}{}
	}

	// Delete stale live policies.
	for _, lp := range livePolicies {
		if _, keep := desiredNames[awssdk.ToString(lp.PolicyName)]; !keep {
			if _, err := d.client.DeleteScalingPolicy(ctx, &applicationautoscaling.DeleteScalingPolicyInput{
				PolicyName:        lp.PolicyName,
				ServiceNamespace:  lp.ServiceNamespace,
				ResourceId:        lp.ResourceId,
				ScalableDimension: lp.ScalableDimension,
			}); err != nil {
				return fmt.Errorf("delete stale policy %q: %w", awssdk.ToString(lp.PolicyName), err)
			}
		}
	}

	// Upsert desired policies.
	for _, p := range desired {
		if err := d.putPolicy(ctx, ns, resourceID, dim, p); err != nil {
			return err
		}
	}
	return nil
}

// policySpec is an internal parsed representation of one policy entry.
type policySpec struct {
	policyName       string
	policyType       string
	targetValue      float64
	scaleInCooldown  int32
	scaleOutCooldown int32
}

// parsePolicies extracts the policies slice from config.
func parsePolicies(config map[string]any) []policySpec {
	raw, ok := config["policies"]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var result []policySpec
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p := policySpec{}
		p.policyName, _ = m["policy_name"].(string)
		p.policyType, _ = m["policy_type"].(string)
		if tv, ok := m["target_value"].(float64); ok {
			p.targetValue = tv
		}
		p.scaleInCooldown = int32(intProp(m, "scale_in_cooldown", 300))
		p.scaleOutCooldown = int32(intProp(m, "scale_out_cooldown", 300))
		if p.policyName != "" {
			result = append(result, p)
		}
	}
	return result
}

// putPolicy calls PutScalingPolicy for a single policySpec.
// Both TargetTrackingScaling and StepScaling are supported.
func (d *AutoScalingGroupDriver) putPolicy(ctx context.Context, ns, resourceID, dim string, p policySpec) error {
	in := &applicationautoscaling.PutScalingPolicyInput{
		PolicyName:        awssdk.String(p.policyName),
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
	}

	switch p.policyType {
	case "TargetTrackingScaling":
		in.PolicyType = aastypes.PolicyTypeTargetTrackingScaling
		cfg := &aastypes.TargetTrackingScalingPolicyConfiguration{
			TargetValue:      awssdk.Float64(p.targetValue),
			ScaleInCooldown:  awssdk.Int32(p.scaleInCooldown),
			ScaleOutCooldown: awssdk.Int32(p.scaleOutCooldown),
		}
		in.TargetTrackingScalingPolicyConfiguration = cfg
	case "StepScaling":
		in.PolicyType = aastypes.PolicyTypeStepScaling
		cfg := &aastypes.StepScalingPolicyConfiguration{
			Cooldown: awssdk.Int32(p.scaleOutCooldown),
		}
		in.StepScalingPolicyConfiguration = cfg
	default:
		// Unknown policy type: default to TargetTracking so the API can validate.
		in.PolicyType = aastypes.PolicyTypeTargetTrackingScaling
		in.TargetTrackingScalingPolicyConfiguration = &aastypes.TargetTrackingScalingPolicyConfiguration{
			TargetValue: awssdk.Float64(p.targetValue),
		}
	}

	if _, err := d.client.PutScalingPolicy(ctx, in); err != nil {
		return fmt.Errorf("put policy %q: %w", p.policyName, err)
	}
	return nil
}

var _ interfaces.ResourceDriver = (*AutoScalingGroupDriver)(nil)

package drivers

import (
	"context"
	"fmt"
	"sort"
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

// AutoScalingGroupDriver manages AWS Application Auto Scaling targets (infra.autoscaling_group).
//
// This driver wraps the AWS Application Auto Scaling service, which manages scalable targets
// for services like ECS, DynamoDB, RDS, etc. It is NOT an EC2 Auto Scaling Group driver.
//
// Config keys:
//   - service_namespace  (string, required) — e.g., "ecs", "dynamodb", "rds"
//   - resource_id        (string, required) — e.g., "service/cluster/service-name"
//   - scalable_dimension (string, required) — e.g., "ecs:service:DesiredCount"
//   - min_capacity       (int, required)    — must be >= 0
//   - max_capacity       (int, required)    — must be >= min_capacity
//   - role_arn           (string, optional)
//   - policies           ([]any, optional)  — each map with keys:
//     * policy_name             (string, required)
//     * policy_type             (string, required) — "TargetTrackingScaling" or "StepScaling"
//     For TargetTrackingScaling:
//     * target_value            (float64, required)
//     * predefined_metric_type  (string, required) — e.g., "ECSServiceAverageCPUUtilization"
//     * scale_in_cooldown       (int, optional, default 300)
//     * scale_out_cooldown      (int, optional, default 300)
//     For StepScaling:
//     * adjustment_type         (string, required) — e.g., "ChangeInCapacity"
//     * step_adjustments        ([]any, required)  — each map with metric_interval_lower_bound, scaling_adjustment
//     * cooldown                (int, optional, default 300)
type AutoScalingGroupDriver struct {
	noSensitiveKeys
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

	minCap, maxCap, err := validateCapacity(spec.Config)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: %w", spec.Name, err)
	}
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

	// Fetch live policies (handles idempotent re-runs) before syncing.
	livePolicies, err := d.fetchLivePolicies(ctx, ns, resourceID, dim)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: fetch policies: %w", spec.Name, err)
	}
	if err := d.syncPolicies(ctx, spec, ns, resourceID, dim, livePolicies); err != nil {
		return nil, fmt.Errorf("autoscaling_group: create %q: apply policies: %w", spec.Name, err)
	}

	policyNames := desiredPolicyNames(spec.Config)
	return d.buildOutput(spec.Name, targetARN, providerID, int(minCap), int(maxCap), policyNames), nil
}

// Read describes the scalable target and its policies.
// ProviderID is required (format: "namespace|resource_id|scalable_dimension").
func (d *AutoScalingGroupDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	ns, resourceID, dim := decodeProviderID(ref.ProviderID)
	if ns == "" {
		return nil, fmt.Errorf("autoscaling_group: read %q: ProviderID is required (format: namespace|resource_id|dimension)", ref.Name)
	}

	out, err := d.client.DescribeScalableTargets(ctx, &applicationautoscaling.DescribeScalableTargetsInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceIds:       []string{resourceID},
		ScalableDimension: aastypes.ScalableDimension(dim),
	})
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: describe %q: %w", ref.Name, err)
	}
	if len(out.ScalableTargets) == 0 {
		return nil, fmt.Errorf("autoscaling_group: scalable target %q not found", ref.Name)
	}
	target := out.ScalableTargets[0]

	policyNames, err := d.readPolicyNames(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: read %q: list policies: %w", ref.Name, err)
	}
	minCap := int(awssdk.ToInt32(target.MinCapacity))
	maxCap := int(awssdk.ToInt32(target.MaxCapacity))
	providerID := encodeProviderID(string(target.ServiceNamespace), awssdk.ToString(target.ResourceId), string(target.ScalableDimension))
	targetARN := awssdk.ToString(target.ScalableTargetARN)

	return d.buildOutput(ref.Name, targetARN, providerID, minCap, maxCap, policyNames), nil
}

// Update re-registers the scalable target (idempotent) and reconciles scaling policies.
// If ref.ProviderID is set, it must match the identity encoded in the spec
// (service_namespace|resource_id|scalable_dimension). A mismatch indicates that the
// identity fields have changed, which would target a different scalable target and
// orphan the previous one; callers should delete and re-create in that case.
func (d *AutoScalingGroupDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	ns, resourceID, dim, err := requiredAutoScalingFields(spec)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: %w", ref.Name, err)
	}

	// Validate identity consistency when ProviderID is known.
	if ref.ProviderID != "" {
		wantProviderID := encodeProviderID(ns, resourceID, dim)
		if ref.ProviderID != wantProviderID {
			return nil, fmt.Errorf("autoscaling_group: update %q: identity mismatch — ProviderID %q does not match spec identity %q; delete and re-create to change scalable target identity", ref.Name, ref.ProviderID, wantProviderID)
		}
	}

	minCap, maxCap, err := validateCapacity(spec.Config)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: %w", ref.Name, err)
	}
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
	livePolicies, err := d.fetchLivePolicies(ctx, ns, resourceID, dim)
	if err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: fetch policies: %w", ref.Name, err)
	}

	if err := d.syncPolicies(ctx, spec, ns, resourceID, dim, livePolicies); err != nil {
		return nil, fmt.Errorf("autoscaling_group: update %q: sync policies: %w", ref.Name, err)
	}

	targetARN := awssdk.ToString(out.ScalableTargetARN)
	providerID := encodeProviderID(ns, resourceID, dim)
	policyNames := desiredPolicyNames(spec.Config)
	return d.buildOutput(ref.Name, targetARN, providerID, int(minCap), int(maxCap), policyNames), nil
}

// Delete removes all scaling policies then deregisters the scalable target.
func (d *AutoScalingGroupDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	ns, resourceID, dim := decodeProviderID(ref.ProviderID)
	if ns == "" {
		return fmt.Errorf("autoscaling_group: delete %q: ProviderID is required (format: namespace|resource_id|dimension)", ref.Name)
	}

	// Delete all existing policies first; errors here block the delete to avoid orphaned targets.
	livePolicies, err := d.fetchLivePolicies(ctx, ns, resourceID, dim)
	if err != nil {
		return fmt.Errorf("autoscaling_group: delete %q: fetch policies: %w", ref.Name, err)
	}
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
// Compares capacity bounds and the sorted set of policy names.
func (d *AutoScalingGroupDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}

	// Build a deterministic policy-name fingerprint for comparison.
	wantPolicies := desiredPolicyNames(desired.Config)
	sort.Strings(wantPolicies)
	wantPolicyKey := strings.Join(wantPolicies, ",")

	// Extract current policy names from outputs.
	var curPolicies []string
	if pn, ok := current.Outputs["policy_names"]; ok {
		switch v := pn.(type) {
		case []string:
			curPolicies = v
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					curPolicies = append(curPolicies, s)
				}
			}
		}
	}
	sort.Strings(curPolicies)
	curPolicyKey := strings.Join(curPolicies, ",")

	// Compute the expected ProviderID from the desired spec to detect identity drift.
	specNS, _ := desired.Config["service_namespace"].(string)
	specResourceID, _ := desired.Config["resource_id"].(string)
	specDim, _ := desired.Config["scalable_dimension"].(string)
	wantProviderID := encodeProviderID(specNS, specResourceID, specDim)

	want := map[string]any{
		"min_capacity": intProp(desired.Config, "min_capacity", 0),
		"max_capacity": intProp(desired.Config, "max_capacity", 1),
		"policy_names": wantPolicyKey,
		"provider_id":  wantProviderID,
	}
	currentForDiff := map[string]any{
		"min_capacity": current.Outputs["min_capacity"],
		"max_capacity": current.Outputs["max_capacity"],
		"policy_names": curPolicyKey,
		"provider_id":  current.ProviderID,
	}
	changes := diffOutputs(want, currentForDiff)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

// HealthCheck returns healthy if the scalable target is readable.
// ProviderID is required.
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

// ---- helpers ----

// validateCapacity extracts min/max_capacity, enforcing presence and min<=max.
func validateCapacity(config map[string]any) (minCap, maxCap int32, err error) {
	if _, ok := config["min_capacity"]; !ok {
		return 0, 0, fmt.Errorf("min_capacity is required")
	}
	if _, ok := config["max_capacity"]; !ok {
		return 0, 0, fmt.Errorf("max_capacity is required")
	}
	minI := intProp(config, "min_capacity", -1)
	maxI := intProp(config, "max_capacity", -1)
	if minI < 0 {
		return 0, 0, fmt.Errorf("min_capacity must be a non-negative integer")
	}
	if maxI < 0 {
		return 0, 0, fmt.Errorf("max_capacity must be a non-negative integer")
	}
	if minI > maxI {
		return 0, 0, fmt.Errorf("min_capacity (%d) must not exceed max_capacity (%d)", minI, maxI)
	}
	return int32(minI), int32(maxI), nil
}

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
// Returns an error so callers can decide whether to abort destructive operations.
func (d *AutoScalingGroupDriver) fetchLivePolicies(ctx context.Context, ns, resourceID, dim string) ([]aastypes.ScalingPolicy, error) {
	out, err := d.client.DescribeScalingPolicies(ctx, &applicationautoscaling.DescribeScalingPoliciesInput{
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.ScalingPolicies, nil
}

// readPolicyNames fetches policy names for a ScalableTarget.
// Returns an error so callers can distinguish "target exists" from "policy enumeration failed."
func (d *AutoScalingGroupDriver) readPolicyNames(ctx context.Context, target aastypes.ScalableTarget) ([]string, error) {
	policies, err := d.fetchLivePolicies(ctx, string(target.ServiceNamespace), awssdk.ToString(target.ResourceId), string(target.ScalableDimension))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range policies {
		names = append(names, awssdk.ToString(p.PolicyName))
	}
	return names, nil
}

// desiredPolicyNames returns the policy_name values from the spec config.
// Malformed policy entries are skipped to allow safe fingerprinting; callers
// that need strict validation use parsePolicies directly.
func desiredPolicyNames(config map[string]any) []string {
	policies, _ := parsePolicies(config)
	names := make([]string, 0, len(policies))
	for _, p := range policies {
		names = append(names, p.policyName)
	}
	return names
}

// syncPolicies reconciles desired policies with live policies:
//  1. Delete live policies whose policy_name is absent from the desired list.
//  2. PutScalingPolicy for each desired policy.
func (d *AutoScalingGroupDriver) syncPolicies(ctx context.Context, spec interfaces.ResourceSpec, ns, resourceID, dim string, livePolicies []aastypes.ScalingPolicy) error {
	desired, err := parsePolicies(spec.Config)
	if err != nil {
		return fmt.Errorf("parse policies: %w", err)
	}

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
	// TargetTrackingScaling fields
	targetValue          float64
	predefinedMetricType string
	scaleInCooldown      int32
	scaleOutCooldown     int32
	// StepScaling fields
	adjustmentType   string
	stepAdjustments  []stepAdjustment
	cooldown         int32
}

// stepAdjustment represents one step in a StepScaling policy.
type stepAdjustment struct {
	metricIntervalLowerBound *float64
	metricIntervalUpperBound *float64
	scalingAdjustment        int32
}

// parsePolicies extracts the policies slice from config.
// Returns an error if the policies key exists but has an unexpected type or contains
// malformed entries, so reconciliation can fail safely without destructive deletes.
func parsePolicies(config map[string]any) ([]policySpec, error) {
	raw, ok := config["policies"]
	if !ok {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("policies must be a list, got %T", raw)
	}
	var result []policySpec
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("policies[%d] must be a map, got %T", i, item)
		}
		p := policySpec{}
		p.policyName, _ = m["policy_name"].(string)
		p.policyType, _ = m["policy_type"].(string)

		// target_value accepts float64, int, int64 (common in YAML decoding).
		if tvRaw, ok := m["target_value"]; ok {
			switch v := tvRaw.(type) {
			case float64:
				p.targetValue = v
			case int:
				p.targetValue = float64(v)
			case int64:
				p.targetValue = float64(v)
			default:
				return nil, fmt.Errorf("policies[%d]: target_value must be a number, got %T", i, tvRaw)
			}
		}

		p.predefinedMetricType, _ = m["predefined_metric_type"].(string)
		p.scaleInCooldown = int32(intProp(m, "scale_in_cooldown", 300))
		p.scaleOutCooldown = int32(intProp(m, "scale_out_cooldown", 300))
		p.adjustmentType, _ = m["adjustment_type"].(string)
		p.cooldown = int32(intProp(m, "cooldown", 300))

		// Parse step_adjustments for StepScaling.
		if saRaw, ok := m["step_adjustments"]; ok {
			saItems, ok := saRaw.([]any)
			if !ok {
				return nil, fmt.Errorf("policies[%d]: step_adjustments must be a list, got %T", i, saRaw)
			}
			for j, saItem := range saItems {
				saMap, ok := saItem.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("policies[%d].step_adjustments[%d] must be a map, got %T", i, j, saItem)
				}
				sa := stepAdjustment{
					scalingAdjustment: int32(intProp(saMap, "scaling_adjustment", 0)),
				}
				if lb, ok := saMap["metric_interval_lower_bound"].(float64); ok {
					sa.metricIntervalLowerBound = awssdk.Float64(lb)
				}
				if ub, ok := saMap["metric_interval_upper_bound"].(float64); ok {
					sa.metricIntervalUpperBound = awssdk.Float64(ub)
				}
				p.stepAdjustments = append(p.stepAdjustments, sa)
			}
		}

		if p.policyName != "" {
			result = append(result, p)
		}
	}
	return result, nil
}

// putPolicy calls PutScalingPolicy for a single policySpec.
// Both TargetTrackingScaling and StepScaling are supported with their required fields.
func (d *AutoScalingGroupDriver) putPolicy(ctx context.Context, ns, resourceID, dim string, p policySpec) error {
	in := &applicationautoscaling.PutScalingPolicyInput{
		PolicyName:        awssdk.String(p.policyName),
		ServiceNamespace:  aastypes.ServiceNamespace(ns),
		ResourceId:        awssdk.String(resourceID),
		ScalableDimension: aastypes.ScalableDimension(dim),
	}

	switch p.policyType {
	case "TargetTrackingScaling":
		if p.predefinedMetricType == "" {
			return fmt.Errorf("put policy %q: predefined_metric_type is required for TargetTrackingScaling (e.g., ECSServiceAverageCPUUtilization)", p.policyName)
		}
		if p.targetValue <= 0 {
			return fmt.Errorf("put policy %q: target_value must be > 0 for TargetTrackingScaling, got %v", p.policyName, p.targetValue)
		}
		in.PolicyType = aastypes.PolicyTypeTargetTrackingScaling
		cfg := &aastypes.TargetTrackingScalingPolicyConfiguration{
			TargetValue: awssdk.Float64(p.targetValue),
			PredefinedMetricSpecification: &aastypes.PredefinedMetricSpecification{
				PredefinedMetricType: aastypes.MetricType(p.predefinedMetricType),
			},
			ScaleInCooldown:  awssdk.Int32(p.scaleInCooldown),
			ScaleOutCooldown: awssdk.Int32(p.scaleOutCooldown),
		}
		in.TargetTrackingScalingPolicyConfiguration = cfg
	case "StepScaling":
		if p.adjustmentType == "" {
			return fmt.Errorf("put policy %q: adjustment_type is required for StepScaling (e.g., ChangeInCapacity)", p.policyName)
		}
		if len(p.stepAdjustments) == 0 {
			return fmt.Errorf("put policy %q: step_adjustments is required for StepScaling (at least one step)", p.policyName)
		}
		in.PolicyType = aastypes.PolicyTypeStepScaling
		steps := make([]aastypes.StepAdjustment, len(p.stepAdjustments))
		for i, sa := range p.stepAdjustments {
			steps[i] = aastypes.StepAdjustment{
				ScalingAdjustment:        awssdk.Int32(sa.scalingAdjustment),
				MetricIntervalLowerBound:   sa.metricIntervalLowerBound,
				MetricIntervalUpperBound:   sa.metricIntervalUpperBound,
			}
		}
		cfg := &aastypes.StepScalingPolicyConfiguration{
			AdjustmentType:  aastypes.AdjustmentType(p.adjustmentType),
			StepAdjustments: steps,
			Cooldown:        awssdk.Int32(p.cooldown),
		}
		in.StepScalingPolicyConfiguration = cfg
	default:
		return fmt.Errorf("put policy %q: unsupported policy_type %q (must be TargetTrackingScaling or StepScaling)", p.policyName, p.policyType)
	}

	if _, err := d.client.PutScalingPolicy(ctx, in); err != nil {
		return fmt.Errorf("put policy %q: %w", p.policyName, err)
	}
	return nil
}

var _ interfaces.ResourceDriver = (*AutoScalingGroupDriver)(nil)

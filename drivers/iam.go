package drivers

import (
	"context"
	"encoding/json"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// IAMClient is the subset of IAM API used by IAMDriver.
type IAMClient interface {
	CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	GetRole(ctx context.Context, params *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	UpdateAssumeRolePolicy(ctx context.Context, params *iam.UpdateAssumeRolePolicyInput, optFns ...func(*iam.Options)) (*iam.UpdateAssumeRolePolicyOutput, error)
	DeleteRole(ctx context.Context, params *iam.DeleteRoleInput, optFns ...func(*iam.Options)) (*iam.DeleteRoleOutput, error)
	AttachRolePolicy(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)
	DetachRolePolicy(ctx context.Context, params *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error)
	ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
}

// IAMDriver manages IAM roles and policies (infra.iam_role).
type IAMDriver struct {
	noSensitiveKeys
	client IAMClient
}

// NewIAMDriver creates an IAM driver from an AWS config.
func NewIAMDriver(cfg awssdk.Config) *IAMDriver {
	return &IAMDriver{client: iam.NewFromConfig(cfg)}
}

// NewIAMDriverWithClient creates an IAM driver with a custom client (for tests).
func NewIAMDriverWithClient(client IAMClient) *IAMDriver {
	return &IAMDriver{client: client}
}

func (d *IAMDriver) ResourceType() string { return "infra.iam_role" }

func (d *IAMDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	assumeRolePolicy, _ := spec.Config["assume_role_policy"].(string)
	if assumeRolePolicy == "" {
		assumeRolePolicy = defaultAssumeRolePolicy()
	}
	description, _ := spec.Config["description"].(string)
	path, _ := spec.Config["path"].(string)
	if path == "" {
		path = "/"
	}

	out, err := d.client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 awssdk.String(spec.Name),
		AssumeRolePolicyDocument: awssdk.String(assumeRolePolicy),
		Description:              awssdk.String(description),
		Path:                     awssdk.String(path),
	})
	if err != nil {
		return nil, fmt.Errorf("iam: create role %q: %w", spec.Name, err)
	}

	policyARNs := stringSliceProp(spec.Config, "policy_arns")
	for _, arn := range policyARNs {
		_, err := d.client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  awssdk.String(spec.Name),
			PolicyArn: awssdk.String(arn),
		})
		if err != nil {
			return nil, fmt.Errorf("iam: attach policy to role %q: %w", spec.Name, err)
		}
	}

	outputs := map[string]any{"policy_arns": policyARNs}
	if out.Role != nil {
		if out.Role.Arn != nil {
			outputs["arn"] = *out.Role.Arn
		}
		if out.Role.RoleId != nil {
			outputs["role_id"] = *out.Role.RoleId
		}
	}

	return &interfaces.ResourceOutput{
		Name:       spec.Name,
		Type:       "infra.iam_role",
		ProviderID: awssdk.ToString(out.Role.Arn),
		Outputs:    outputs,
		Status:     "running",
	}, nil
}

func (d *IAMDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.GetRole(ctx, &iam.GetRoleInput{RoleName: awssdk.String(ref.Name)})
	if err != nil {
		return nil, fmt.Errorf("iam: get role %q: %w", ref.Name, err)
	}

	outputs := map[string]any{}
	arn := ""
	if out.Role != nil {
		if out.Role.Arn != nil {
			outputs["arn"] = *out.Role.Arn
			arn = *out.Role.Arn
		}
		if out.Role.RoleId != nil {
			outputs["role_id"] = *out.Role.RoleId
		}
		if out.Role.Description != nil {
			outputs["description"] = *out.Role.Description
		}
	}

	polOut, err := d.client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: awssdk.String(ref.Name),
	})
	if err == nil && polOut != nil {
		var arns []string
		for _, p := range polOut.AttachedPolicies {
			if p.PolicyArn != nil {
				arns = append(arns, *p.PolicyArn)
			}
		}
		outputs["policy_arns"] = arns
	}

	return &interfaces.ResourceOutput{
		Name:       ref.Name,
		Type:       "infra.iam_role",
		ProviderID: arn,
		Outputs:    outputs,
		Status:     "running",
	}, nil
}

func (d *IAMDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if policy, _ := spec.Config["assume_role_policy"].(string); policy != "" {
		_, err := d.client.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
			RoleName:       awssdk.String(ref.Name),
			PolicyDocument: awssdk.String(policy),
		})
		if err != nil {
			return nil, fmt.Errorf("iam: update assume role policy %q: %w", ref.Name, err)
		}
	}

	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}

	desiredPolicies := stringSliceProp(spec.Config, "policy_arns")
	currentPolicies := stringSliceProp(current.Outputs, "policy_arns")

	for _, arn := range diffSlice(desiredPolicies, currentPolicies) {
		_, err := d.client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  awssdk.String(ref.Name),
			PolicyArn: awssdk.String(arn),
		})
		if err != nil {
			return nil, fmt.Errorf("iam: attach policy %q to %q: %w", arn, ref.Name, err)
		}
	}
	for _, arn := range diffSlice(currentPolicies, desiredPolicies) {
		_, err := d.client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  awssdk.String(ref.Name),
			PolicyArn: awssdk.String(arn),
		})
		if err != nil {
			return nil, fmt.Errorf("iam: detach policy %q from %q: %w", arn, ref.Name, err)
		}
	}

	return d.Read(ctx, ref)
}

func (d *IAMDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	polOut, err := d.client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: awssdk.String(ref.Name),
	})
	if err == nil && polOut != nil {
		for _, p := range polOut.AttachedPolicies {
			_, _ = d.client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
				RoleName:  awssdk.String(ref.Name),
				PolicyArn: p.PolicyArn,
			})
		}
	}

	_, err = d.client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: awssdk.String(ref.Name)})
	if err != nil {
		return fmt.Errorf("iam: delete role %q: %w", ref.Name, err)
	}
	return nil
}

func (d *IAMDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *IAMDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "role exists"}, nil
}

func (d *IAMDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("iam: IAM roles are not scalable")
}

func defaultAssumeRolePolicy() string {
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"Service": "lambda.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			},
		},
	}
	data, _ := json.Marshal(policy)
	return string(data)
}

var _ interfaces.ResourceDriver = (*IAMDriver)(nil)

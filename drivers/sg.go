package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// SGClient is the subset of EC2 API used by SecurityGroupDriver.
type SGClient interface {
	CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DeleteSecurityGroup(ctx context.Context, params *ec2.DeleteSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	RevokeSecurityGroupIngress(ctx context.Context, params *ec2.RevokeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// SecurityGroupDriver manages EC2 security groups (infra.firewall).
type SecurityGroupDriver struct {
	client SGClient
}

// NewSecurityGroupDriver creates a security group driver from an AWS config.
func NewSecurityGroupDriver(cfg awssdk.Config) *SecurityGroupDriver {
	return &SecurityGroupDriver{client: ec2.NewFromConfig(cfg)}
}

// NewSecurityGroupDriverWithClient creates a security group driver with a custom client (for tests).
func NewSecurityGroupDriverWithClient(client SGClient) *SecurityGroupDriver {
	return &SecurityGroupDriver{client: client}
}

func (d *SecurityGroupDriver) ResourceType() string { return "infra.firewall" }

func (d *SecurityGroupDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	description, _ := spec.Config["description"].(string)
	if description == "" {
		description = fmt.Sprintf("Managed by workflow: %s", spec.Name)
	}
	vpcID, _ := spec.Config["vpc_id"].(string)

	in := &ec2.CreateSecurityGroupInput{
		GroupName:   awssdk.String(spec.Name),
		Description: awssdk.String(description),
	}
	if vpcID != "" {
		in.VpcId = awssdk.String(vpcID)
	}

	out, err := d.client.CreateSecurityGroup(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("sg: create %q: %w", spec.Name, err)
	}
	groupID := awssdk.ToString(out.GroupId)

	// Tag it with the name
	_, _ = d.client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{groupID},
		Tags:      []ec2types.Tag{{Key: awssdk.String("Name"), Value: awssdk.String(spec.Name)}},
	})

	// Add ingress rules if specified
	if rules, ok := spec.Config["ingress_rules"].([]any); ok {
		perms := parseIPPerms(rules)
		if len(perms) > 0 {
			_, err = d.client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
				GroupId:       awssdk.String(groupID),
				IpPermissions: perms,
			})
			if err != nil {
				return nil, fmt.Errorf("sg: authorize ingress %q: %w", spec.Name, err)
			}
		}
	}

	return sgToOutput(spec.Name, groupID, vpcID), nil
}

func (d *SecurityGroupDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	in := &ec2.DescribeSecurityGroupsInput{}
	if ref.ProviderID != "" {
		in.GroupIds = []string{ref.ProviderID}
	} else {
		in.Filters = []ec2types.Filter{
			{Name: awssdk.String("tag:Name"), Values: []string{ref.Name}},
		}
	}
	out, err := d.client.DescribeSecurityGroups(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("sg: describe %q: %w", ref.Name, err)
	}
	if len(out.SecurityGroups) == 0 {
		return nil, fmt.Errorf("sg: %q not found", ref.Name)
	}
	sg := &out.SecurityGroups[0]
	vpcID := awssdk.ToString(sg.VpcId)
	groupID := awssdk.ToString(sg.GroupId)
	return sgToOutput(ref.Name, groupID, vpcID), nil
}

func (d *SecurityGroupDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	groupID, _ := current.Outputs["group_id"].(string)
	if groupID == "" {
		return nil, fmt.Errorf("sg: update %q: no group_id", ref.Name)
	}

	// Revoke existing rules and re-apply
	out, err := d.client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{groupID},
	})
	if err == nil && len(out.SecurityGroups) > 0 {
		existing := out.SecurityGroups[0].IpPermissions
		if len(existing) > 0 {
			_, _ = d.client.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
				GroupId:       awssdk.String(groupID),
				IpPermissions: existing,
			})
		}
	}

	if rules, ok := spec.Config["ingress_rules"].([]any); ok {
		perms := parseIPPerms(rules)
		if len(perms) > 0 {
			_, err = d.client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
				GroupId:       awssdk.String(groupID),
				IpPermissions: perms,
			})
			if err != nil {
				return nil, fmt.Errorf("sg: re-authorize ingress %q: %w", ref.Name, err)
			}
		}
	}

	vpcID, _ := current.Outputs["vpc_id"].(string)
	return sgToOutput(ref.Name, groupID, vpcID), nil
}

func (d *SecurityGroupDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return err
	}
	groupID, _ := current.Outputs["group_id"].(string)
	if groupID == "" {
		return fmt.Errorf("sg: delete %q: no group_id", ref.Name)
	}
	_, err = d.client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: awssdk.String(groupID),
	})
	if err != nil {
		return fmt.Errorf("sg: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *SecurityGroupDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *SecurityGroupDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "security group exists"}, nil
}

func (d *SecurityGroupDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("sg: security groups are not scalable")
}

func parseIPPerms(rules []any) []ec2types.IpPermission {
	var perms []ec2types.IpPermission
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		fromPort := int32(intProp(rule, "from_port", 0))
		toPort := int32(intProp(rule, "to_port", 0))
		proto, _ := rule["protocol"].(string)
		if proto == "" {
			proto = "tcp"
		}
		cidr, _ := rule["cidr"].(string)
		if cidr == "" {
			cidr = "0.0.0.0/0"
		}
		perms = append(perms, ec2types.IpPermission{
			IpProtocol: awssdk.String(proto),
			FromPort:   awssdk.Int32(fromPort),
			ToPort:     awssdk.Int32(toPort),
			IpRanges:   []ec2types.IpRange{{CidrIp: awssdk.String(cidr)}},
		})
	}
	return perms
}

func sgToOutput(name, groupID, vpcID string) *interfaces.ResourceOutput {
	outputs := map[string]any{"group_id": groupID}
	if vpcID != "" {
		outputs["vpc_id"] = vpcID
	}
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.firewall",
		ProviderID: groupID,
		Outputs:    outputs,
		Status:     "running",
	}
}

var _ interfaces.ResourceDriver = (*SecurityGroupDriver)(nil)

package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// VPCClient is the subset of EC2 API used by VPCDriver.
type VPCClient interface {
	CreateVpc(ctx context.Context, params *ec2.CreateVpcInput, optFns ...func(*ec2.Options)) (*ec2.CreateVpcOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DeleteVpc(ctx context.Context, params *ec2.DeleteVpcInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// VPCDriver manages AWS VPC resources (infra.vpc).
type VPCDriver struct {
	client VPCClient
}

// NewVPCDriver creates a VPC driver from an AWS config.
func NewVPCDriver(cfg awssdk.Config) *VPCDriver {
	return &VPCDriver{client: ec2.NewFromConfig(cfg)}
}

// NewVPCDriverWithClient creates a VPC driver with a custom client (for tests).
func NewVPCDriverWithClient(client VPCClient) *VPCDriver {
	return &VPCDriver{client: client}
}

func (d *VPCDriver) ResourceType() string { return "infra.vpc" }

func (d *VPCDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	cidr, _ := spec.Config["cidr"].(string)
	if cidr == "" {
		cidr = "10.0.0.0/16"
	}

	out, err := d.client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: awssdk.String(cidr),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeVpc,
				Tags:         []ec2types.Tag{{Key: awssdk.String("Name"), Value: awssdk.String(spec.Name)}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("vpc: create %q: %w", spec.Name, err)
	}
	return vpcToOutput(spec.Name, out.Vpc), nil
}

func (d *VPCDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	filter := ec2.DescribeVpcsInput{}
	if ref.ProviderID != "" {
		filter.VpcIds = []string{ref.ProviderID}
	} else {
		filter.Filters = []ec2types.Filter{
			{Name: awssdk.String("tag:Name"), Values: []string{ref.Name}},
		}
	}

	out, err := d.client.DescribeVpcs(ctx, &filter)
	if err != nil {
		return nil, fmt.Errorf("vpc: describe %q: %w", ref.Name, err)
	}
	if len(out.Vpcs) == 0 {
		return nil, fmt.Errorf("vpc: %q not found", ref.Name)
	}
	return vpcToOutput(ref.Name, &out.Vpcs[0]), nil
}

func (d *VPCDriver) Update(ctx context.Context, ref interfaces.ResourceRef, _ interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	// VPC CIDR cannot change; only tags can be updated
	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	vpcID, _ := current.Outputs["vpc_id"].(string)
	if vpcID != "" {
		_, err = d.client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{vpcID},
			Tags:      []ec2types.Tag{{Key: awssdk.String("Name"), Value: awssdk.String(ref.Name)}},
		})
		if err != nil {
			return nil, fmt.Errorf("vpc: update tags %q: %w", ref.Name, err)
		}
	}
	return d.Read(ctx, ref)
}

func (d *VPCDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return err
	}
	vpcID, _ := current.Outputs["vpc_id"].(string)
	if vpcID == "" {
		return fmt.Errorf("vpc: cannot delete %q: no vpc_id in state", ref.Name)
	}
	_, err = d.client.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: awssdk.String(vpcID)})
	if err != nil {
		return fmt.Errorf("vpc: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *VPCDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *VPCDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	state, _ := out.Outputs["state"].(string)
	healthy := state == "available"
	return &interfaces.HealthResult{Healthy: healthy, Message: state}, nil
}

func (d *VPCDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("vpc: scaling is not applicable")
}

func vpcToOutput(name string, vpc *ec2types.Vpc) *interfaces.ResourceOutput {
	if vpc == nil {
		return nil
	}
	state := string(vpc.State)
	status := "running"
	if vpc.State == ec2types.VpcStatePending {
		status = "creating"
	}

	outputs := map[string]any{"state": state}
	if vpc.VpcId != nil {
		outputs["vpc_id"] = *vpc.VpcId
	}
	if vpc.CidrBlock != nil {
		outputs["cidr"] = *vpc.CidrBlock
	}

	providerID := ""
	if vpc.VpcId != nil {
		providerID = *vpc.VpcId
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.vpc",
		ProviderID: providerID,
		Outputs:    outputs,
		Status:     status,
	}
}

// SensitiveKeys returns output keys whose values should be masked in logs and plan output.
func (d *VPCDriver) SensitiveKeys() []string { return nil }

var _ interfaces.ResourceDriver = (*VPCDriver)(nil)

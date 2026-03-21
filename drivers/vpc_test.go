package drivers_test

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockVPCClient struct {
	createOut  *ec2.CreateVpcOutput
	createErr  error
	describeOut *ec2.DescribeVpcsOutput
	describeErr error
	deleteErr   error
	tagsErr     error
}

func (m *mockVPCClient) CreateVpc(_ context.Context, _ *ec2.CreateVpcInput, _ ...func(*ec2.Options)) (*ec2.CreateVpcOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockVPCClient) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockVPCClient) DeleteVpc(_ context.Context, _ *ec2.DeleteVpcInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error) {
	return &ec2.DeleteVpcOutput{}, m.deleteErr
}
func (m *mockVPCClient) CreateTags(_ context.Context, _ *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, m.tagsErr
}

func TestVPCDriver_Create(t *testing.T) {
	mock := &mockVPCClient{
		createOut: &ec2.CreateVpcOutput{
			Vpc: &ec2types.Vpc{
				VpcId:     awssdk.String("vpc-12345"),
				CidrBlock: awssdk.String("10.0.0.0/16"),
				State:     ec2types.VpcStateAvailable,
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{"cidr": "10.0.0.0/16"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.vpc" {
		t.Errorf("expected infra.vpc, got %s", out.Type)
	}
	if out.ProviderID != "vpc-12345" {
		t.Errorf("expected vpc-12345, got %s", out.ProviderID)
	}
}

func TestVPCDriver_Read_ByTag(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{
					VpcId:     awssdk.String("vpc-12345"),
					CidrBlock: awssdk.String("10.0.0.0/16"),
					State:     ec2types.VpcStateAvailable,
				},
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-vpc"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Outputs["vpc_id"] != "vpc-12345" {
		t.Errorf("unexpected vpc_id: %v", out.Outputs["vpc_id"])
	}
}

func TestVPCDriver_Delete(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{
					VpcId:     awssdk.String("vpc-12345"),
					CidrBlock: awssdk.String("10.0.0.0/16"),
					State:     ec2types.VpcStateAvailable,
				},
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-vpc"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestVPCDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewVPCDriverWithClient(&mockVPCClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "vpc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true")
	}
}

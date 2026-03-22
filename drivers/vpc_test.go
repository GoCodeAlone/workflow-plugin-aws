package drivers_test

import (
	"context"
	"fmt"
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

func TestVPCDriver_Create_Error(t *testing.T) {
	mock := &mockVPCClient{createErr: fmt.Errorf("VPC limit exceeded")}
	d := drivers.NewVPCDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{"cidr": "10.0.0.0/16"},
	})
	if err == nil {
		t.Fatal("expected error on CreateVpc API failure")
	}
}

func TestVPCDriver_Update_Success(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: awssdk.String("vpc-12345"), CidrBlock: awssdk.String("10.0.0.0/16"), State: ec2types.VpcStateAvailable},
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-vpc"}, interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{"cidr": "10.0.0.0/16"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestVPCDriver_Update_Error(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: awssdk.String("vpc-12345"), CidrBlock: awssdk.String("10.0.0.0/16"), State: ec2types.VpcStateAvailable},
			},
		},
		tagsErr: fmt.Errorf("tag limit exceeded"),
	}
	d := drivers.NewVPCDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-vpc"}, interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error on CreateTags API failure")
	}
}

func TestVPCDriver_Delete_Error(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: awssdk.String("vpc-12345"), CidrBlock: awssdk.String("10.0.0.0/16"), State: ec2types.VpcStateAvailable},
			},
		},
		deleteErr: fmt.Errorf("vpc has dependencies"),
	}
	d := drivers.NewVPCDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-vpc"})
	if err == nil {
		t.Fatal("expected error on DeleteVpc API failure")
	}
}

func TestVPCDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewVPCDriverWithClient(&mockVPCClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-vpc",
		Type:    "infra.vpc",
		Outputs: map[string]any{"cidr": "10.0.0.0/16"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{"cidr": "10.1.0.0/16"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when cidr changes")
	}
}

func TestVPCDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewVPCDriverWithClient(&mockVPCClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-vpc",
		Type:    "infra.vpc",
		Outputs: map[string]any{"cidr": "10.0.0.0/16"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-vpc",
		Config: map[string]any{"cidr": "10.0.0.0/16"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestVPCDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: awssdk.String("vpc-12345"), CidrBlock: awssdk.String("10.0.0.0/16"), State: ec2types.VpcStateAvailable},
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-vpc"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy, got: %s", h.Message)
	}
}

func TestVPCDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockVPCClient{
		describeOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: awssdk.String("vpc-12345"), CidrBlock: awssdk.String("10.0.0.0/16"), State: ec2types.VpcStatePending},
			},
		},
	}
	d := drivers.NewVPCDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-vpc"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy for pending VPC")
	}
}

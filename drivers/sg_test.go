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

type mockSGClient struct {
	createOut    *ec2.CreateSecurityGroupOutput
	createErr    error
	describeOut  *ec2.DescribeSecurityGroupsOutput
	describeErr  error
	deleteErr    error
	authorizeErr error
	revokeErr    error
	tagsErr      error
}

func (m *mockSGClient) CreateSecurityGroup(_ context.Context, _ *ec2.CreateSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockSGClient) DescribeSecurityGroups(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockSGClient) DeleteSecurityGroup(_ context.Context, _ *ec2.DeleteSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	return &ec2.DeleteSecurityGroupOutput{}, m.deleteErr
}
func (m *mockSGClient) AuthorizeSecurityGroupIngress(_ context.Context, _ *ec2.AuthorizeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, m.authorizeErr
}
func (m *mockSGClient) RevokeSecurityGroupIngress(_ context.Context, _ *ec2.RevokeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return &ec2.RevokeSecurityGroupIngressOutput{}, m.revokeErr
}
func (m *mockSGClient) CreateTags(_ context.Context, _ *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, m.tagsErr
}

func TestSGDriver_Create(t *testing.T) {
	mock := &mockSGClient{
		createOut: &ec2.CreateSecurityGroupOutput{
			GroupId: awssdk.String("sg-12345"),
		},
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-sg",
		Config: map[string]any{"vpc_id": "vpc-abc"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.firewall" {
		t.Errorf("expected infra.firewall, got %s", out.Type)
	}
	if out.ProviderID != "sg-12345" {
		t.Errorf("expected sg-12345, got %s", out.ProviderID)
	}
}

func TestSGDriver_Read(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{
					GroupId:  awssdk.String("sg-12345"),
					GroupName: awssdk.String("my-sg"),
					VpcId:    awssdk.String("vpc-abc"),
				},
			},
		},
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-sg"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Outputs["group_id"] != "sg-12345" {
		t.Errorf("unexpected group_id: %v", out.Outputs["group_id"])
	}
}

func TestSGDriver_Delete(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: awssdk.String("sg-12345"), GroupName: awssdk.String("my-sg")},
			},
		},
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-sg"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestSGDriver_Scale_ReturnsError(t *testing.T) {
	d := drivers.NewSecurityGroupDriverWithClient(&mockSGClient{})
	_, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-sg"}, 1)
	if err == nil {
		t.Error("expected error from Scale on security group")
	}
}

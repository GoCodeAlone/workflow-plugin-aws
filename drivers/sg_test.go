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

func TestSGDriver_Create_Error(t *testing.T) {
	mock := &mockSGClient{createErr: fmt.Errorf("VPC not found")}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-sg",
		Config: map[string]any{"vpc_id": "vpc-invalid"},
	})
	if err == nil {
		t.Fatal("expected error on CreateSecurityGroup API failure")
	}
}

func TestSGDriver_Update_Success(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: awssdk.String("sg-12345"), GroupName: awssdk.String("my-sg"), VpcId: awssdk.String("vpc-abc")},
			},
		},
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-sg"}, interfaces.ResourceSpec{
		Name:   "my-sg",
		Config: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestSGDriver_Update_Error(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: awssdk.String("sg-12345"), GroupName: awssdk.String("my-sg")},
			},
		},
		authorizeErr: fmt.Errorf("duplicate ingress rule"),
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-sg"}, interfaces.ResourceSpec{
		Name: "my-sg",
		Config: map[string]any{
			"ingress_rules": []any{
				map[string]any{"from_port": 80, "to_port": 80, "protocol": "tcp"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error on AuthorizeSecurityGroupIngress API failure")
	}
}

func TestSGDriver_Delete_Error(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: awssdk.String("sg-12345"), GroupName: awssdk.String("my-sg")},
			},
		},
		deleteErr: fmt.Errorf("security group in use"),
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-sg"})
	if err == nil {
		t.Fatal("expected error on DeleteSecurityGroup API failure")
	}
}

func TestSGDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewSecurityGroupDriverWithClient(&mockSGClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "my-sg"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestSGDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewSecurityGroupDriverWithClient(&mockSGClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-sg",
		Type:    "infra.firewall",
		Outputs: map[string]any{"vpc_id": "vpc-old"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-sg",
		Config: map[string]any{"vpc_id": "vpc-new"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when vpc_id changes")
	}
}

func TestSGDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewSecurityGroupDriverWithClient(&mockSGClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-sg",
		Type:    "infra.firewall",
		Outputs: map[string]any{"vpc_id": "vpc-abc"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-sg",
		Config: map[string]any{"vpc_id": "vpc-abc"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestSGDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockSGClient{
		describeOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: awssdk.String("sg-12345"), GroupName: awssdk.String("my-sg"), VpcId: awssdk.String("vpc-abc")},
			},
		},
	}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-sg"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy, got: %s", h.Message)
	}
}

func TestSGDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockSGClient{describeErr: fmt.Errorf("security group not found")}
	d := drivers.NewSecurityGroupDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "missing-sg"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy when security group not found")
	}
	if h.Message == "" {
		t.Error("expected non-empty message for unhealthy security group")
	}
}

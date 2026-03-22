package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockIAMClient struct {
	createOut   *iam.CreateRoleOutput
	createErr   error
	getOut      *iam.GetRoleOutput
	getErr      error
	updateErr   error
	deleteErr   error
	attachErr   error
	detachErr   error
	listPolOut  *iam.ListAttachedRolePoliciesOutput
	listPolErr  error
}

func (m *mockIAMClient) CreateRole(_ context.Context, _ *iam.CreateRoleInput, _ ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockIAMClient) GetRole(_ context.Context, _ *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return m.getOut, m.getErr
}
func (m *mockIAMClient) UpdateAssumeRolePolicy(_ context.Context, _ *iam.UpdateAssumeRolePolicyInput, _ ...func(*iam.Options)) (*iam.UpdateAssumeRolePolicyOutput, error) {
	return &iam.UpdateAssumeRolePolicyOutput{}, m.updateErr
}
func (m *mockIAMClient) DeleteRole(_ context.Context, _ *iam.DeleteRoleInput, _ ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	return &iam.DeleteRoleOutput{}, m.deleteErr
}
func (m *mockIAMClient) AttachRolePolicy(_ context.Context, _ *iam.AttachRolePolicyInput, _ ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
	return &iam.AttachRolePolicyOutput{}, m.attachErr
}
func (m *mockIAMClient) DetachRolePolicy(_ context.Context, _ *iam.DetachRolePolicyInput, _ ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	return &iam.DetachRolePolicyOutput{}, m.detachErr
}
func (m *mockIAMClient) ListAttachedRolePolicies(_ context.Context, _ *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return m.listPolOut, m.listPolErr
}

func TestIAMDriver_Create(t *testing.T) {
	roleARN := "arn:aws:iam::123:role/my-role"
	mock := &mockIAMClient{
		createOut: &iam.CreateRoleOutput{
			Role: &iamtypes.Role{
				RoleName: awssdk.String("my-role"),
				Arn:      awssdk.String(roleARN),
				RoleId:   awssdk.String("AROAEXAMPLE"),
			},
		},
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
	}
	d := drivers.NewIAMDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.ProviderID != roleARN {
		t.Errorf("expected ProviderID %s, got %s", roleARN, out.ProviderID)
	}
}

func TestIAMDriver_Read(t *testing.T) {
	roleARN := "arn:aws:iam::123:role/my-role"
	mock := &mockIAMClient{
		getOut: &iam.GetRoleOutput{
			Role: &iamtypes.Role{
				RoleName: awssdk.String("my-role"),
				Arn:      awssdk.String(roleARN),
				RoleId:   awssdk.String("AROAEXAMPLE"),
			},
		},
		listPolOut: &iam.ListAttachedRolePoliciesOutput{
			AttachedPolicies: []iamtypes.AttachedPolicy{
				{PolicyArn: awssdk.String("arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess")},
			},
		},
	}
	d := drivers.NewIAMDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-role"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Outputs["arn"] != roleARN {
		t.Errorf("unexpected arn: %v", out.Outputs["arn"])
	}
}

func TestIAMDriver_Delete(t *testing.T) {
	mock := &mockIAMClient{
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
	}
	d := drivers.NewIAMDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-role"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestIAMDriver_HealthCheck(t *testing.T) {
	mock := &mockIAMClient{
		getOut: &iam.GetRoleOutput{
			Role: &iamtypes.Role{
				RoleName: awssdk.String("my-role"),
				Arn:      awssdk.String("arn:..."),
			},
		},
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
	}
	d := drivers.NewIAMDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-role"})
	if err != nil {
		t.Fatal(err)
	}
	if !health.Healthy {
		t.Errorf("expected healthy")
	}
}

func TestIAMDriver_Create_Error(t *testing.T) {
	mock := &mockIAMClient{createErr: fmt.Errorf("role already exists")}
	d := drivers.NewIAMDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error on CreateRole API failure")
	}
}

func TestIAMDriver_Update_Success(t *testing.T) {
	roleARN := "arn:aws:iam::123:role/my-role"
	mock := &mockIAMClient{
		getOut: &iam.GetRoleOutput{
			Role: &iamtypes.Role{
				RoleName: awssdk.String("my-role"),
				Arn:      awssdk.String(roleARN),
				RoleId:   awssdk.String("AROAEXAMPLE"),
			},
		},
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
	}
	d := drivers.NewIAMDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-role"}, interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{"assume_role_policy": `{"Version":"2012-10-17","Statement":[]}`},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestIAMDriver_Update_Error(t *testing.T) {
	roleARN := "arn:aws:iam::123:role/my-role"
	mock := &mockIAMClient{
		getOut: &iam.GetRoleOutput{
			Role: &iamtypes.Role{
				RoleName: awssdk.String("my-role"),
				Arn:      awssdk.String(roleARN),
			},
		},
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
		updateErr:  fmt.Errorf("malformed policy document"),
	}
	d := drivers.NewIAMDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-role"}, interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{"assume_role_policy": "invalid-json"},
	})
	if err == nil {
		t.Fatal("expected error on UpdateAssumeRolePolicy API failure")
	}
}

func TestIAMDriver_Delete_Error(t *testing.T) {
	mock := &mockIAMClient{
		listPolOut: &iam.ListAttachedRolePoliciesOutput{},
		deleteErr:  fmt.Errorf("role not found"),
	}
	d := drivers.NewIAMDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "missing-role"})
	if err == nil {
		t.Fatal("expected error on DeleteRole API failure")
	}
}

func TestIAMDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewIAMDriverWithClient(&mockIAMClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "my-role"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestIAMDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewIAMDriverWithClient(&mockIAMClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-role",
		Type:    "infra.iam_role",
		Outputs: map[string]any{"arn": "arn:aws:iam::123:role/my-role"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{"arn": "arn:aws:iam::456:role/my-role"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when arn changes")
	}
}

func TestIAMDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewIAMDriverWithClient(&mockIAMClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-role",
		Type:    "infra.iam_role",
		Outputs: map[string]any{"arn": "arn:aws:iam::123:role/my-role"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-role",
		Config: map[string]any{"arn": "arn:aws:iam::123:role/my-role"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestIAMDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockIAMClient{getErr: fmt.Errorf("role not found")}
	d := drivers.NewIAMDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "missing-role"})
	if err != nil {
		t.Fatal(err)
	}
	if health.Healthy {
		t.Error("expected unhealthy when role not found")
	}
	if health.Message == "" {
		t.Error("expected non-empty message for unhealthy role")
	}
}

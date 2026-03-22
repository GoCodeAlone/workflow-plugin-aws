package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockRDSClient struct {
	createOut *rds.CreateDBInstanceOutput
	createErr error
	describeOut *rds.DescribeDBInstancesOutput
	describeErr error
	modifyOut   *rds.ModifyDBInstanceOutput
	modifyErr   error
	deleteErr   error
}

func (m *mockRDSClient) CreateDBInstance(_ context.Context, _ *rds.CreateDBInstanceInput, _ ...func(*rds.Options)) (*rds.CreateDBInstanceOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockRDSClient) DescribeDBInstances(_ context.Context, _ *rds.DescribeDBInstancesInput, _ ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockRDSClient) ModifyDBInstance(_ context.Context, _ *rds.ModifyDBInstanceInput, _ ...func(*rds.Options)) (*rds.ModifyDBInstanceOutput, error) {
	return m.modifyOut, m.modifyErr
}
func (m *mockRDSClient) DeleteDBInstance(_ context.Context, _ *rds.DeleteDBInstanceInput, _ ...func(*rds.Options)) (*rds.DeleteDBInstanceOutput, error) {
	return &rds.DeleteDBInstanceOutput{}, m.deleteErr
}

func TestRDSDriver_Create(t *testing.T) {
	mock := &mockRDSClient{
		createOut: &rds.CreateDBInstanceOutput{
			DBInstance: &rdstypes.DBInstance{
				DBInstanceIdentifier: awssdk.String("my-db"),
				DBInstanceStatus:     awssdk.String("creating"),
				Engine:               awssdk.String("postgres"),
				DBInstanceClass:      awssdk.String("db.t3.micro"),
				DBInstanceArn:        awssdk.String("arn:aws:rds:us-east-1:123:db:my-db"),
			},
		},
	}
	d := drivers.NewRDSDriverWithClient(mock)
	spec := interfaces.ResourceSpec{
		Name: "my-db",
		Type: "infra.database",
		Config: map[string]any{
			"engine":          "postgres",
			"instance_class":  "db.t3.micro",
			"master_password": "secret",
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Name != "my-db" {
		t.Errorf("expected name my-db, got %s", out.Name)
	}
	if out.Type != "infra.database" {
		t.Errorf("expected type infra.database, got %s", out.Type)
	}
}

func TestRDSDriver_Create_MissingPassword(t *testing.T) {
	d := drivers.NewRDSDriverWithClient(&mockRDSClient{})
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "db",
		Config: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing master_password")
	}
}

func TestRDSDriver_Read(t *testing.T) {
	mock := &mockRDSClient{
		describeOut: &rds.DescribeDBInstancesOutput{
			DBInstances: []rdstypes.DBInstance{
				{
					DBInstanceIdentifier: awssdk.String("my-db"),
					DBInstanceStatus:     awssdk.String("available"),
					Engine:               awssdk.String("postgres"),
					DBInstanceClass:      awssdk.String("db.t3.micro"),
					DBInstanceArn:        awssdk.String("arn:aws:rds:us-east-1:123:db:my-db"),
				},
			},
		},
	}
	d := drivers.NewRDSDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-db"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-db" {
		t.Errorf("expected my-db, got %s", out.Name)
	}
}

func TestRDSDriver_Delete(t *testing.T) {
	d := drivers.NewRDSDriverWithClient(&mockRDSClient{})
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-db"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestRDSDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockRDSClient{
		describeOut: &rds.DescribeDBInstancesOutput{
			DBInstances: []rdstypes.DBInstance{
				{
					DBInstanceIdentifier: awssdk.String("my-db"),
					DBInstanceStatus:     awssdk.String("available"),
					DBInstanceArn:        awssdk.String("arn:aws:rds:us-east-1:123:db:my-db"),
				},
			},
		},
	}
	d := drivers.NewRDSDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-db"})
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if !health.Healthy {
		t.Errorf("expected healthy, got unhealthy: %s", health.Message)
	}
}

func TestRDSDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewRDSDriverWithClient(&mockRDSClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestRDSDriver_Create_APIError(t *testing.T) {
	mock := &mockRDSClient{createErr: fmt.Errorf("db identifier already exists")}
	d := drivers.NewRDSDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-db",
		Config: map[string]any{"master_password": "secret"},
	})
	if err == nil {
		t.Fatal("expected error on CreateDBInstance API failure")
	}
}

func TestRDSDriver_Update_Success(t *testing.T) {
	mock := &mockRDSClient{
		modifyOut: &rds.ModifyDBInstanceOutput{
			DBInstance: &rdstypes.DBInstance{
				DBInstanceIdentifier: awssdk.String("my-db"),
				DBInstanceStatus:     awssdk.String("modifying"),
				DBInstanceClass:      awssdk.String("db.t3.small"),
				DBInstanceArn:        awssdk.String("arn:aws:rds:us-east-1:123:db:my-db"),
			},
		},
	}
	d := drivers.NewRDSDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-db"}, interfaces.ResourceSpec{
		Name:   "my-db",
		Config: map[string]any{"instance_class": "db.t3.small"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestRDSDriver_Update_Error(t *testing.T) {
	mock := &mockRDSClient{modifyErr: fmt.Errorf("invalid parameter")}
	d := drivers.NewRDSDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-db"}, interfaces.ResourceSpec{
		Name:   "my-db",
		Config: map[string]any{"instance_class": "db.invalid"},
	})
	if err == nil {
		t.Fatal("expected error on ModifyDBInstance API failure")
	}
}

func TestRDSDriver_Delete_Error(t *testing.T) {
	mock := &mockRDSClient{deleteErr: fmt.Errorf("cannot delete protected instance")}
	d := drivers.NewRDSDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-db"})
	if err == nil {
		t.Fatal("expected error on DeleteDBInstance API failure")
	}
}

func TestRDSDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewRDSDriverWithClient(&mockRDSClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-db",
		Type:    "infra.database",
		Outputs: map[string]any{"instance_class": "db.t3.micro"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-db",
		Config: map[string]any{"instance_class": "db.t3.small"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when instance_class changes")
	}
}

func TestRDSDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewRDSDriverWithClient(&mockRDSClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-db",
		Type:    "infra.database",
		Outputs: map[string]any{"instance_class": "db.t3.micro"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-db",
		Config: map[string]any{"instance_class": "db.t3.micro"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestRDSDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockRDSClient{
		describeOut: &rds.DescribeDBInstancesOutput{
			DBInstances: []rdstypes.DBInstance{
				{
					DBInstanceIdentifier: awssdk.String("my-db"),
					DBInstanceStatus:     awssdk.String("stopped"),
					DBInstanceArn:        awssdk.String("arn:aws:rds:us-east-1:123:db:my-db"),
				},
			},
		},
	}
	d := drivers.NewRDSDriverWithClient(mock)
	health, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-db"})
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if health.Healthy {
		t.Error("expected unhealthy for stopped instance")
	}
	if health.Message == "" {
		t.Error("expected non-empty message for unhealthy instance")
	}
}

package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockElastiCacheClient struct {
	createOut  *elasticache.CreateReplicationGroupOutput
	createErr  error
	describeOut *elasticache.DescribeReplicationGroupsOutput
	describeErr error
	modifyOut   *elasticache.ModifyReplicationGroupOutput
	modifyErr   error
	deleteErr   error
}

func (m *mockElastiCacheClient) CreateReplicationGroup(_ context.Context, _ *elasticache.CreateReplicationGroupInput, _ ...func(*elasticache.Options)) (*elasticache.CreateReplicationGroupOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockElastiCacheClient) DescribeReplicationGroups(_ context.Context, _ *elasticache.DescribeReplicationGroupsInput, _ ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockElastiCacheClient) ModifyReplicationGroup(_ context.Context, _ *elasticache.ModifyReplicationGroupInput, _ ...func(*elasticache.Options)) (*elasticache.ModifyReplicationGroupOutput, error) {
	return m.modifyOut, m.modifyErr
}
func (m *mockElastiCacheClient) DeleteReplicationGroup(_ context.Context, _ *elasticache.DeleteReplicationGroupInput, _ ...func(*elasticache.Options)) (*elasticache.DeleteReplicationGroupOutput, error) {
	return &elasticache.DeleteReplicationGroupOutput{}, m.deleteErr
}

func TestElastiCacheDriver_Create(t *testing.T) {
	status := "creating"
	mock := &mockElastiCacheClient{
		createOut: &elasticache.CreateReplicationGroupOutput{
			ReplicationGroup: &ectypes.ReplicationGroup{
				ReplicationGroupId: awssdk.String("my-cache"),
				Description:        awssdk.String("my-cache"),
				Status:             awssdk.String(status),
			},
		},
	}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"node_type": "cache.t3.micro"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.cache" {
		t.Errorf("expected infra.cache, got %s", out.Type)
	}
}

func TestElastiCacheDriver_Read(t *testing.T) {
	mock := &mockElastiCacheClient{
		describeOut: &elasticache.DescribeReplicationGroupsOutput{
			ReplicationGroups: []ectypes.ReplicationGroup{
				{
					ReplicationGroupId: awssdk.String("my-cache"),
					Status:             awssdk.String("available"),
				},
			},
		},
	}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-cache"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-cache" {
		t.Errorf("expected my-cache, got %s", out.Name)
	}
}

func TestElastiCacheDriver_Delete(t *testing.T) {
	d := drivers.NewElastiCacheDriverWithClient(&mockElastiCacheClient{})
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-cache"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestElastiCacheDriver_Scale_ReturnsError(t *testing.T) {
	d := drivers.NewElastiCacheDriverWithClient(&mockElastiCacheClient{})
	_, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-cache"}, 3)
	if err == nil {
		t.Error("expected error from Scale")
	}
}

func TestElastiCacheDriver_HealthCheck_Available(t *testing.T) {
	mock := &mockElastiCacheClient{
		describeOut: &elasticache.DescribeReplicationGroupsOutput{
			ReplicationGroups: []ectypes.ReplicationGroup{
				{
					ReplicationGroupId: awssdk.String("my-cache"),
					Status:             awssdk.String("available"),
				},
			},
		},
	}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy")
	}
}

func TestElastiCacheDriver_Create_Error(t *testing.T) {
	mock := &mockElastiCacheClient{createErr: fmt.Errorf("replication group already exists")}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"node_type": "cache.t3.micro"},
	})
	if err == nil {
		t.Fatal("expected error on CreateReplicationGroup API failure")
	}
}

func TestElastiCacheDriver_Update_Success(t *testing.T) {
	mock := &mockElastiCacheClient{
		modifyOut: &elasticache.ModifyReplicationGroupOutput{
			ReplicationGroup: &ectypes.ReplicationGroup{
				ReplicationGroupId: awssdk.String("my-cache"),
				Status:             awssdk.String("modifying"),
			},
		},
	}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-cache"}, interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"automatic_failover": true},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestElastiCacheDriver_Update_Error(t *testing.T) {
	mock := &mockElastiCacheClient{modifyErr: fmt.Errorf("invalid parameter combination")}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-cache"}, interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"automatic_failover": true},
	})
	if err == nil {
		t.Fatal("expected error on ModifyReplicationGroup API failure")
	}
}

func TestElastiCacheDriver_Delete_Error(t *testing.T) {
	mock := &mockElastiCacheClient{deleteErr: fmt.Errorf("replication group not found")}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-cache"})
	if err == nil {
		t.Fatal("expected error on DeleteReplicationGroup API failure")
	}
}

func TestElastiCacheDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewElastiCacheDriverWithClient(&mockElastiCacheClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "my-cache"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestElastiCacheDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewElastiCacheDriverWithClient(&mockElastiCacheClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-cache",
		Type:    "infra.cache",
		Outputs: map[string]any{"node_type": "cache.t3.micro"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"node_type": "cache.t3.small"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when node_type changes")
	}
}

func TestElastiCacheDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewElastiCacheDriverWithClient(&mockElastiCacheClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-cache",
		Type:    "infra.cache",
		Outputs: map[string]any{"node_type": "cache.t3.micro"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cache",
		Config: map[string]any{"node_type": "cache.t3.micro"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestElastiCacheDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockElastiCacheClient{
		describeOut: &elasticache.DescribeReplicationGroupsOutput{
			ReplicationGroups: []ectypes.ReplicationGroup{
				{
					ReplicationGroupId: awssdk.String("my-cache"),
					Status:             awssdk.String("creating"),
				},
			},
		},
	}
	d := drivers.NewElastiCacheDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy for non-available status")
	}
	if h.Message == "" {
		t.Error("expected non-empty message for unhealthy cache")
	}
}

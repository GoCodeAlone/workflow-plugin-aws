package drivers_test

import (
	"context"
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

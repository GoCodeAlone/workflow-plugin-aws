package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockEKSClient struct {
	createOut  *eks.CreateClusterOutput
	createErr  error
	describeOut *eks.DescribeClusterOutput
	describeErr error
	updateErr   error
	deleteErr   error
}

func (m *mockEKSClient) CreateCluster(_ context.Context, _ *eks.CreateClusterInput, _ ...func(*eks.Options)) (*eks.CreateClusterOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockEKSClient) DescribeCluster(_ context.Context, _ *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockEKSClient) UpdateClusterVersion(_ context.Context, _ *eks.UpdateClusterVersionInput, _ ...func(*eks.Options)) (*eks.UpdateClusterVersionOutput, error) {
	return &eks.UpdateClusterVersionOutput{}, m.updateErr
}
func (m *mockEKSClient) DeleteCluster(_ context.Context, _ *eks.DeleteClusterInput, _ ...func(*eks.Options)) (*eks.DeleteClusterOutput, error) {
	return &eks.DeleteClusterOutput{}, m.deleteErr
}

func TestEKSDriver_Create(t *testing.T) {
	arn := "arn:aws:eks:us-east-1:123:cluster/my-cluster"
	mock := &mockEKSClient{
		createOut: &eks.CreateClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name:     awssdk.String("my-cluster"),
				Arn:      awssdk.String(arn),
				Status:   ekstypes.ClusterStatusCreating,
				Version:  awssdk.String("1.29"),
				Endpoint: awssdk.String("https://api.example.com"),
			},
		},
	}
	d := drivers.NewEKSDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.29"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.k8s_cluster" {
		t.Errorf("expected infra.k8s_cluster, got %s", out.Type)
	}
	if out.ProviderID != arn {
		t.Errorf("expected ProviderID %s, got %s", arn, out.ProviderID)
	}
}

func TestEKSDriver_Read(t *testing.T) {
	mock := &mockEKSClient{
		describeOut: &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name:    awssdk.String("my-cluster"),
				Arn:     awssdk.String("arn:aws:eks:us-east-1:123:cluster/my-cluster"),
				Status:  ekstypes.ClusterStatusActive,
				Version: awssdk.String("1.29"),
			},
		},
	}
	d := drivers.NewEKSDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-cluster"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Status != "running" {
		t.Errorf("expected running, got %s", out.Status)
	}
}

func TestEKSDriver_Delete(t *testing.T) {
	d := drivers.NewEKSDriverWithClient(&mockEKSClient{})
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-cluster"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestEKSDriver_HealthCheck_Active(t *testing.T) {
	mock := &mockEKSClient{
		describeOut: &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name:   awssdk.String("my-cluster"),
				Status: ekstypes.ClusterStatusActive,
			},
		},
	}
	d := drivers.NewEKSDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-cluster"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy")
	}
}

func TestEKSDriver_Scale_ReturnsError(t *testing.T) {
	d := drivers.NewEKSDriverWithClient(&mockEKSClient{})
	_, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-cluster"}, 3)
	if err == nil {
		t.Error("expected error from Scale on EKS cluster")
	}
}

func TestEKSDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewEKSDriverWithClient(&mockEKSClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "cluster"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestEKSDriver_Read_Error(t *testing.T) {
	mock := &mockEKSClient{describeErr: fmt.Errorf("not found")}
	d := drivers.NewEKSDriverWithClient(mock)
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "missing"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestEKSDriver_Create_Error(t *testing.T) {
	mock := &mockEKSClient{createErr: fmt.Errorf("cluster already exists")}
	d := drivers.NewEKSDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.29"},
	})
	if err == nil {
		t.Fatal("expected error on CreateCluster API failure")
	}
}

func TestEKSDriver_Update_Success(t *testing.T) {
	mock := &mockEKSClient{
		updateErr: nil,
		describeOut: &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name:    awssdk.String("my-cluster"),
				Arn:     awssdk.String("arn:aws:eks:us-east-1:123:cluster/my-cluster"),
				Status:  ekstypes.ClusterStatusActive,
				Version: awssdk.String("1.30"),
			},
		},
	}
	d := drivers.NewEKSDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-cluster"}, interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.30"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestEKSDriver_Update_Error(t *testing.T) {
	mock := &mockEKSClient{updateErr: fmt.Errorf("update in progress")}
	d := drivers.NewEKSDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-cluster"}, interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.30"},
	})
	if err == nil {
		t.Fatal("expected error on UpdateClusterVersion API failure")
	}
}

func TestEKSDriver_Delete_Error(t *testing.T) {
	mock := &mockEKSClient{deleteErr: fmt.Errorf("cluster not found")}
	d := drivers.NewEKSDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-cluster"})
	if err == nil {
		t.Fatal("expected error on DeleteCluster API failure")
	}
}

func TestEKSDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewEKSDriverWithClient(&mockEKSClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-cluster",
		Type:    "infra.k8s_cluster",
		Outputs: map[string]any{"version": "1.28"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.29"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when version changes")
	}
}

func TestEKSDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewEKSDriverWithClient(&mockEKSClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-cluster",
		Type:    "infra.k8s_cluster",
		Outputs: map[string]any{"version": "1.29"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-cluster",
		Config: map[string]any{"version": "1.29"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestEKSDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockEKSClient{
		describeOut: &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name:   awssdk.String("my-cluster"),
				Status: ekstypes.ClusterStatusCreating,
			},
		},
	}
	d := drivers.NewEKSDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-cluster"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy for CREATING cluster")
	}
	if h.Message == "" {
		t.Error("expected non-empty message for unhealthy cluster")
	}
}

package drivers_test

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockECRClient struct {
	createOut   *ecr.CreateRepositoryOutput
	createErr   error
	describeOut *ecr.DescribeRepositoriesOutput
	describeErr error
	deleteErr   error
	policyErr   error
}

func (m *mockECRClient) CreateRepository(_ context.Context, _ *ecr.CreateRepositoryInput, _ ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockECRClient) DescribeRepositories(_ context.Context, _ *ecr.DescribeRepositoriesInput, _ ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockECRClient) DeleteRepository(_ context.Context, _ *ecr.DeleteRepositoryInput, _ ...func(*ecr.Options)) (*ecr.DeleteRepositoryOutput, error) {
	return &ecr.DeleteRepositoryOutput{}, m.deleteErr
}
func (m *mockECRClient) PutLifecyclePolicy(_ context.Context, _ *ecr.PutLifecyclePolicyInput, _ ...func(*ecr.Options)) (*ecr.PutLifecyclePolicyOutput, error) {
	return &ecr.PutLifecyclePolicyOutput{}, m.policyErr
}

func TestECRDriver_Create(t *testing.T) {
	repoARN := "arn:aws:ecr:us-east-1:123:repository/my-repo"
	mock := &mockECRClient{
		createOut: &ecr.CreateRepositoryOutput{
			Repository: &ecrtypes.Repository{
				RepositoryName: awssdk.String("my-repo"),
				RepositoryArn:  awssdk.String(repoARN),
				RepositoryUri:  awssdk.String("123.dkr.ecr.us-east-1.amazonaws.com/my-repo"),
				RegistryId:     awssdk.String("123"),
			},
		},
	}
	d := drivers.NewECRDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-repo",
		Config: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.registry" {
		t.Errorf("expected infra.registry, got %s", out.Type)
	}
	if out.ProviderID != repoARN {
		t.Errorf("expected ProviderID %s, got %s", repoARN, out.ProviderID)
	}
}

func TestECRDriver_Read(t *testing.T) {
	mock := &mockECRClient{
		describeOut: &ecr.DescribeRepositoriesOutput{
			Repositories: []ecrtypes.Repository{
				{
					RepositoryName: awssdk.String("my-repo"),
					RepositoryArn:  awssdk.String("arn:aws:ecr:us-east-1:123:repository/my-repo"),
					RepositoryUri:  awssdk.String("123.dkr.ecr.us-east-1.amazonaws.com/my-repo"),
				},
			},
		},
	}
	d := drivers.NewECRDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-repo"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-repo" {
		t.Errorf("expected my-repo, got %s", out.Name)
	}
}

func TestECRDriver_Delete(t *testing.T) {
	d := drivers.NewECRDriverWithClient(&mockECRClient{})
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-repo"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestECRDriver_HealthCheck(t *testing.T) {
	mock := &mockECRClient{
		describeOut: &ecr.DescribeRepositoriesOutput{
			Repositories: []ecrtypes.Repository{
				{RepositoryName: awssdk.String("my-repo"), RepositoryArn: awssdk.String("arn:...")},
			},
		},
	}
	d := drivers.NewECRDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy")
	}
}

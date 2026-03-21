package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// ECRClient is the subset of ECR API used by ECRDriver.
type ECRClient interface {
	CreateRepository(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error)
	DescribeRepositories(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	DeleteRepository(ctx context.Context, params *ecr.DeleteRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.DeleteRepositoryOutput, error)
	PutLifecyclePolicy(ctx context.Context, params *ecr.PutLifecyclePolicyInput, optFns ...func(*ecr.Options)) (*ecr.PutLifecyclePolicyOutput, error)
}

// ECRDriver manages ECR repositories (infra.registry).
type ECRDriver struct {
	client ECRClient
}

// NewECRDriver creates an ECR driver from an AWS config.
func NewECRDriver(cfg awssdk.Config) *ECRDriver {
	return &ECRDriver{client: ecr.NewFromConfig(cfg)}
}

// NewECRDriverWithClient creates an ECR driver with a custom client (for tests).
func NewECRDriverWithClient(client ECRClient) *ECRDriver {
	return &ECRDriver{client: client}
}

func (d *ECRDriver) ResourceType() string { return "infra.registry" }

func (d *ECRDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	out, err := d.client.CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName: awssdk.String(spec.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("ecr: create repository %q: %w", spec.Name, err)
	}
	return ecrRepoToOutput(out.Repository), nil
}

func (d *ECRDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{ref.Name},
	})
	if err != nil {
		return nil, fmt.Errorf("ecr: describe repository %q: %w", ref.Name, err)
	}
	if len(out.Repositories) == 0 {
		return nil, fmt.Errorf("ecr: repository %q not found", ref.Name)
	}
	return ecrRepoToOutput(&out.Repositories[0]), nil
}

func (d *ECRDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if policy, _ := spec.Config["lifecycle_policy"].(string); policy != "" {
		_, err := d.client.PutLifecyclePolicy(ctx, &ecr.PutLifecyclePolicyInput{
			RepositoryName:      awssdk.String(ref.Name),
			LifecyclePolicyText: awssdk.String(policy),
		})
		if err != nil {
			return nil, fmt.Errorf("ecr: set lifecycle policy %q: %w", ref.Name, err)
		}
	}
	return d.Read(ctx, ref)
}

func (d *ECRDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	_, err := d.client.DeleteRepository(ctx, &ecr.DeleteRepositoryInput{
		RepositoryName: awssdk.String(ref.Name),
		Force:          true,
	})
	if err != nil {
		return fmt.Errorf("ecr: delete repository %q: %w", ref.Name, err)
	}
	return nil
}

func (d *ECRDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *ECRDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "repository exists"}, nil
}

func (d *ECRDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("ecr: registries are not scalable")
}

func ecrRepoToOutput(repo *ecrtypes.Repository) *interfaces.ResourceOutput {
	if repo == nil {
		return nil
	}
	outputs := map[string]any{}
	if repo.RepositoryArn != nil {
		outputs["arn"] = *repo.RepositoryArn
	}
	if repo.RepositoryUri != nil {
		outputs["uri"] = *repo.RepositoryUri
		outputs["endpoint"] = *repo.RepositoryUri
	}
	if repo.RegistryId != nil {
		outputs["registry_id"] = *repo.RegistryId
	}

	name := awssdk.ToString(repo.RepositoryName)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.registry",
		ProviderID: awssdk.ToString(repo.RepositoryArn),
		Outputs:    outputs,
		Status:     "running",
	}
}

var _ interfaces.ResourceDriver = (*ECRDriver)(nil)

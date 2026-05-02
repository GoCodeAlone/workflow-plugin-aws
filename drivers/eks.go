package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// EKSClient is the subset of EKS API used by EKSDriver.
type EKSClient interface {
	CreateCluster(ctx context.Context, params *eks.CreateClusterInput, optFns ...func(*eks.Options)) (*eks.CreateClusterOutput, error)
	DescribeCluster(ctx context.Context, params *eks.DescribeClusterInput, optFns ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
	UpdateClusterVersion(ctx context.Context, params *eks.UpdateClusterVersionInput, optFns ...func(*eks.Options)) (*eks.UpdateClusterVersionOutput, error)
	DeleteCluster(ctx context.Context, params *eks.DeleteClusterInput, optFns ...func(*eks.Options)) (*eks.DeleteClusterOutput, error)
}

// EKSDriver manages EKS clusters (infra.k8s_cluster).
type EKSDriver struct {
	noSensitiveKeys
	client EKSClient
}

// NewEKSDriver creates an EKS driver from an AWS config.
func NewEKSDriver(cfg awssdk.Config) *EKSDriver {
	return &EKSDriver{client: eks.NewFromConfig(cfg)}
}

// NewEKSDriverWithClient creates an EKS driver with a custom client (for tests).
func NewEKSDriverWithClient(client EKSClient) *EKSDriver {
	return &EKSDriver{client: client}
}

func (d *EKSDriver) ResourceType() string { return "infra.k8s_cluster" }

func (d *EKSDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	version, _ := spec.Config["version"].(string)
	if version == "" {
		version = "1.29"
	}
	roleARN, _ := spec.Config["role_arn"].(string)
	subnetIDs := stringSliceProp(spec.Config, "subnet_ids")
	sgIDs := stringSliceProp(spec.Config, "security_group_ids")

	in := &eks.CreateClusterInput{
		Name:    awssdk.String(spec.Name),
		Version: awssdk.String(version),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds:        subnetIDs,
			SecurityGroupIds: sgIDs,
		},
	}
	if roleARN != "" {
		in.RoleArn = awssdk.String(roleARN)
	}

	out, err := d.client.CreateCluster(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("eks: create cluster %q: %w", spec.Name, err)
	}
	return eksClusterToOutput(out.Cluster), nil
}

func (d *EKSDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: awssdk.String(ref.Name)})
	if err != nil {
		return nil, fmt.Errorf("eks: describe cluster %q: %w", ref.Name, err)
	}
	return eksClusterToOutput(out.Cluster), nil
}

func (d *EKSDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if version, _ := spec.Config["version"].(string); version != "" {
		_, err := d.client.UpdateClusterVersion(ctx, &eks.UpdateClusterVersionInput{
			Name:    awssdk.String(ref.Name),
			Version: awssdk.String(version),
		})
		if err != nil {
			return nil, fmt.Errorf("eks: update cluster version %q: %w", ref.Name, err)
		}
	}
	return d.Read(ctx, ref)
}

func (d *EKSDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	_, err := d.client.DeleteCluster(ctx, &eks.DeleteClusterInput{Name: awssdk.String(ref.Name)})
	if err != nil {
		return fmt.Errorf("eks: delete cluster %q: %w", ref.Name, err)
	}
	return nil
}

func (d *EKSDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *EKSDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: awssdk.String(ref.Name)})
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	healthy := out.Cluster.Status == ekstypes.ClusterStatusActive
	return &interfaces.HealthResult{Healthy: healthy, Message: string(out.Cluster.Status)}, nil
}

func (d *EKSDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("eks: cluster scaling is managed via node groups, not directly on the cluster")
}

func eksClusterToOutput(c *ekstypes.Cluster) *interfaces.ResourceOutput {
	if c == nil {
		return nil
	}
	status := "running"
	switch c.Status {
	case ekstypes.ClusterStatusCreating:
		status = "creating"
	case ekstypes.ClusterStatusDeleting:
		status = "deleting"
	case ekstypes.ClusterStatusFailed:
		status = "failed"
	case ekstypes.ClusterStatusUpdating:
		status = "updating"
	}

	outputs := map[string]any{"status": string(c.Status)}
	if c.Version != nil {
		outputs["version"] = *c.Version
	}
	if c.Arn != nil {
		outputs["arn"] = *c.Arn
	}
	if c.Endpoint != nil {
		outputs["endpoint"] = *c.Endpoint
	}

	name := awssdk.ToString(c.Name)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.k8s_cluster",
		ProviderID: awssdk.ToString(c.Arn),
		Outputs:    outputs,
		Status:     status,
	}
}

var _ interfaces.ResourceDriver = (*EKSDriver)(nil)

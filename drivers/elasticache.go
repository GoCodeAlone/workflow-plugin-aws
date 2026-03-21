package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// ElastiCacheClient is the subset of ElastiCache API used by ElastiCacheDriver.
type ElastiCacheClient interface {
	CreateReplicationGroup(ctx context.Context, params *elasticache.CreateReplicationGroupInput, optFns ...func(*elasticache.Options)) (*elasticache.CreateReplicationGroupOutput, error)
	DescribeReplicationGroups(ctx context.Context, params *elasticache.DescribeReplicationGroupsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error)
	ModifyReplicationGroup(ctx context.Context, params *elasticache.ModifyReplicationGroupInput, optFns ...func(*elasticache.Options)) (*elasticache.ModifyReplicationGroupOutput, error)
	DeleteReplicationGroup(ctx context.Context, params *elasticache.DeleteReplicationGroupInput, optFns ...func(*elasticache.Options)) (*elasticache.DeleteReplicationGroupOutput, error)
}

// ElastiCacheDriver manages ElastiCache replication groups (infra.cache).
type ElastiCacheDriver struct {
	client ElastiCacheClient
}

// NewElastiCacheDriver creates an ElastiCache driver from an AWS config.
func NewElastiCacheDriver(cfg awssdk.Config) *ElastiCacheDriver {
	return &ElastiCacheDriver{client: elasticache.NewFromConfig(cfg)}
}

// NewElastiCacheDriverWithClient creates an ElastiCache driver with a custom client (for tests).
func NewElastiCacheDriverWithClient(client ElastiCacheClient) *ElastiCacheDriver {
	return &ElastiCacheDriver{client: client}
}

func (d *ElastiCacheDriver) ResourceType() string { return "infra.cache" }

func (d *ElastiCacheDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	nodeType, _ := spec.Config["node_type"].(string)
	if nodeType == "" {
		nodeType = "cache.t3.micro"
	}
	engine, _ := spec.Config["engine"].(string)
	if engine == "" {
		engine = "redis"
	}
	engineVersion, _ := spec.Config["engine_version"].(string)
	numReplicas := int32(intProp(spec.Config, "num_cache_clusters", 1))
	description, _ := spec.Config["description"].(string)
	if description == "" {
		description = spec.Name
	}

	in := &elasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          awssdk.String(spec.Name),
		ReplicationGroupDescription: awssdk.String(description),
		CacheNodeType:               awssdk.String(nodeType),
		Engine:                      awssdk.String(engine),
		NumCacheClusters:            awssdk.Int32(numReplicas),
		AutomaticFailoverEnabled:    awssdk.Bool(numReplicas > 1),
	}
	if engineVersion != "" {
		in.EngineVersion = awssdk.String(engineVersion)
	}

	out, err := d.client.CreateReplicationGroup(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("elasticache: create %q: %w", spec.Name, err)
	}
	return ecRGToOutput(out.ReplicationGroup), nil
}

func (d *ElastiCacheDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeReplicationGroups(ctx, &elasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: awssdk.String(ref.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("elasticache: describe %q: %w", ref.Name, err)
	}
	if len(out.ReplicationGroups) == 0 {
		return nil, fmt.Errorf("elasticache: replication group %q not found", ref.Name)
	}
	return ecRGToOutput(&out.ReplicationGroups[0]), nil
}

func (d *ElastiCacheDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	in := &elasticache.ModifyReplicationGroupInput{
		ReplicationGroupId: awssdk.String(ref.Name),
		ApplyImmediately:   awssdk.Bool(true),
	}
	if nodeType, _ := spec.Config["node_type"].(string); nodeType != "" {
		in.CacheNodeType = awssdk.String(nodeType)
	}

	out, err := d.client.ModifyReplicationGroup(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("elasticache: modify %q: %w", ref.Name, err)
	}
	return ecRGToOutput(out.ReplicationGroup), nil
}

func (d *ElastiCacheDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	_, err := d.client.DeleteReplicationGroup(ctx, &elasticache.DeleteReplicationGroupInput{
		ReplicationGroupId: awssdk.String(ref.Name),
	})
	if err != nil {
		return fmt.Errorf("elasticache: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *ElastiCacheDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *ElastiCacheDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	status, _ := out.Outputs["status"].(string)
	healthy := status == "available"
	return &interfaces.HealthResult{Healthy: healthy, Message: status}, nil
}

func (d *ElastiCacheDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("elasticache: replica count changes require IncreaseReplicaCount/DecreaseReplicaCount; use Update with num_cache_clusters")
}

func ecRGToOutput(rg *ectypes.ReplicationGroup) *interfaces.ResourceOutput {
	if rg == nil {
		return nil
	}
	rgStatus := awssdk.ToString(rg.Status)
	status := "running"
	switch rgStatus {
	case "creating":
		status = "creating"
	case "deleting":
		status = "deleting"
	case "modifying":
		status = "updating"
	case "snapshotting":
		status = "updating"
	}

	outputs := map[string]any{"status": rgStatus}
	if rg.Description != nil {
		outputs["description"] = *rg.Description
	}
	if rg.ReplicationGroupId != nil {
		outputs["replication_group_id"] = *rg.ReplicationGroupId
	}

	endpoint := ""
	if rg.ConfigurationEndpoint != nil && rg.ConfigurationEndpoint.Address != nil {
		endpoint = fmt.Sprintf("%s:%d", *rg.ConfigurationEndpoint.Address, rg.ConfigurationEndpoint.Port)
		outputs["endpoint"] = endpoint
	}

	name := awssdk.ToString(rg.ReplicationGroupId)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.cache",
		ProviderID: name,
		Outputs:    outputs,
		Status:     status,
	}
}

var _ interfaces.ResourceDriver = (*ElastiCacheDriver)(nil)

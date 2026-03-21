// Package provider implements the AWS IaCProvider for workflow.
package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

const (
	ProviderName    = "aws"
	ProviderVersion = "0.1.0"
)

// AWSProvider implements interfaces.IaCProvider for Amazon Web Services.
type AWSProvider struct {
	mu          sync.RWMutex
	initialized bool
	region      string
	cfg         awssdk.Config
	driverMap   map[string]interfaces.ResourceDriver
}

// NewAWSProvider creates a new AWS provider.
func NewAWSProvider() interfaces.IaCProvider {
	return &AWSProvider{
		driverMap: make(map[string]interfaces.ResourceDriver),
	}
}

func (p *AWSProvider) Name() string    { return ProviderName }
func (p *AWSProvider) Version() string { return ProviderVersion }

// Initialize configures the AWS SDK and registers all resource drivers.
// Supported config keys: region, access_key_id, secret_access_key, ecs_cluster.
func (p *AWSProvider) Initialize(ctx context.Context, config map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	region, _ := config["region"].(string)
	if region == "" {
		region = "us-east-1"
	}
	p.region = region

	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(region),
	}
	accessKey, _ := config["access_key_id"].(string)
	secretKey, _ := config["secret_access_key"].(string)
	if accessKey != "" && secretKey != "" {
		opts = append(opts, awscfg.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("aws: load config: %w", err)
	}
	p.cfg = cfg

	ecsCluster, _ := config["ecs_cluster"].(string)
	p.registerDrivers(cfg, ecsCluster, region)
	p.initialized = true
	return nil
}

func (p *AWSProvider) registerDrivers(cfg awssdk.Config, ecsCluster, region string) {
	driverList := []interface {
		ResourceType() string
		interfaces.ResourceDriver
	}{
		drivers.NewECSDriver(cfg, ecsCluster),
		drivers.NewEKSDriver(cfg),
		drivers.NewRDSDriver(cfg),
		drivers.NewElastiCacheDriver(cfg),
		drivers.NewVPCDriver(cfg),
		drivers.NewALBDriver(cfg),
		drivers.NewRoute53Driver(cfg),
		drivers.NewECRDriver(cfg),
		drivers.NewAPIGatewayDriver(cfg),
		drivers.NewSecurityGroupDriver(cfg),
		drivers.NewIAMDriver(cfg),
		drivers.NewS3Driver(cfg, region),
		drivers.NewACMDriver(cfg),
	}
	for _, d := range driverList {
		p.driverMap[d.ResourceType()] = d
	}
}

func (p *AWSProvider) Capabilities() []interfaces.IaCCapabilityDeclaration {
	return []interfaces.IaCCapabilityDeclaration{
		{ResourceType: "infra.container_service", Tier: 1, Operations: []string{"create", "read", "update", "delete", "scale"}},
		{ResourceType: "infra.k8s_cluster", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.database", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.cache", Tier: 1, Operations: []string{"create", "read", "update", "delete", "scale"}},
		{ResourceType: "infra.vpc", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.load_balancer", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.dns", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.registry", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.api_gateway", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.firewall", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.iam_role", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.storage", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.certificate", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
	}
}

func (p *AWSProvider) ResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.driverMap[resourceType]
	if !ok {
		return nil, fmt.Errorf("aws: no driver for resource type %q", resourceType)
	}
	return d, nil
}

// Plan computes required create/update/delete actions for the desired state.
func (p *AWSProvider) Plan(ctx context.Context, desired []interfaces.ResourceSpec, current []interfaces.ResourceState) (*interfaces.IaCPlan, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	currentMap := make(map[string]*interfaces.ResourceState, len(current))
	for i := range current {
		currentMap[current[i].Name] = &current[i]
	}

	plan := &interfaces.IaCPlan{
		ID:        fmt.Sprintf("plan-%d", time.Now().UnixNano()),
		CreatedAt: time.Now(),
	}

	for _, spec := range desired {
		cur := currentMap[spec.Name]
		if cur == nil {
			plan.Actions = append(plan.Actions, interfaces.PlanAction{
				Action:   "create",
				Resource: spec,
			})
			continue
		}

		drv, err := p.resourceDriver(spec.Type)
		if err != nil {
			return nil, err
		}
		curOutput := &interfaces.ResourceOutput{
			Name:       cur.Name,
			Type:       cur.Type,
			ProviderID: cur.ProviderID,
			Outputs:    cur.Outputs,
		}
		diff, err := drv.Diff(ctx, spec, curOutput)
		if err != nil {
			return nil, fmt.Errorf("aws: diff %q: %w", spec.Name, err)
		}
		if diff.NeedsUpdate || diff.NeedsReplace {
			action := "update"
			if diff.NeedsReplace {
				action = "replace"
			}
			plan.Actions = append(plan.Actions, interfaces.PlanAction{
				Action:   action,
				Resource: spec,
				Current:  cur,
				Changes:  diff.Changes,
			})
		}
	}

	return plan, nil
}

// Apply executes the plan actions.
func (p *AWSProvider) Apply(ctx context.Context, plan *interfaces.IaCPlan) (*interfaces.ApplyResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	result := &interfaces.ApplyResult{PlanID: plan.ID}

	for _, action := range plan.Actions {
		drv, err := p.resourceDriver(action.Resource.Type)
		if err != nil {
			result.Errors = append(result.Errors, interfaces.ActionError{
				Resource: action.Resource.Name,
				Action:   action.Action,
				Error:    err.Error(),
			})
			continue
		}

		var out *interfaces.ResourceOutput
		switch action.Action {
		case "create":
			out, err = drv.Create(ctx, action.Resource)
		case "update", "replace":
			ref := interfaces.ResourceRef{Name: action.Resource.Name, Type: action.Resource.Type}
			if action.Current != nil {
				ref.ProviderID = action.Current.ProviderID
			}
			out, err = drv.Update(ctx, ref, action.Resource)
		case "delete":
			ref := interfaces.ResourceRef{Name: action.Resource.Name, Type: action.Resource.Type}
			if action.Current != nil {
				ref.ProviderID = action.Current.ProviderID
			}
			err = drv.Delete(ctx, ref)
		}

		if err != nil {
			result.Errors = append(result.Errors, interfaces.ActionError{
				Resource: action.Resource.Name,
				Action:   action.Action,
				Error:    err.Error(),
			})
			continue
		}
		if out != nil {
			result.Resources = append(result.Resources, *out)
		}
	}

	return result, nil
}

// Destroy deletes a set of resources.
func (p *AWSProvider) Destroy(ctx context.Context, resources []interfaces.ResourceRef) (*interfaces.DestroyResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	result := &interfaces.DestroyResult{}
	for _, ref := range resources {
		drv, err := p.resourceDriver(ref.Type)
		if err != nil {
			result.Errors = append(result.Errors, interfaces.ActionError{
				Resource: ref.Name,
				Action:   "delete",
				Error:    err.Error(),
			})
			continue
		}
		if err := drv.Delete(ctx, ref); err != nil {
			result.Errors = append(result.Errors, interfaces.ActionError{
				Resource: ref.Name,
				Action:   "delete",
				Error:    err.Error(),
			})
		} else {
			result.Destroyed = append(result.Destroyed, ref.Name)
		}
	}
	return result, nil
}

// Status returns the live status of the given resources.
func (p *AWSProvider) Status(ctx context.Context, resources []interfaces.ResourceRef) ([]interfaces.ResourceStatus, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	var statuses []interfaces.ResourceStatus
	for _, ref := range resources {
		drv, err := p.resourceDriver(ref.Type)
		if err != nil {
			statuses = append(statuses, interfaces.ResourceStatus{
				Name:   ref.Name,
				Type:   ref.Type,
				Status: "unknown",
			})
			continue
		}
		out, err := drv.Read(ctx, ref)
		if err != nil {
			statuses = append(statuses, interfaces.ResourceStatus{
				Name:   ref.Name,
				Type:   ref.Type,
				Status: "unknown",
			})
			continue
		}
		statuses = append(statuses, interfaces.ResourceStatus{
			Name:       ref.Name,
			Type:       ref.Type,
			ProviderID: out.ProviderID,
			Status:     out.Status,
			Outputs:    out.Outputs,
		})
	}
	return statuses, nil
}

// DetectDrift compares declared state against live AWS state.
func (p *AWSProvider) DetectDrift(ctx context.Context, resources []interfaces.ResourceRef) ([]interfaces.DriftResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	var results []interfaces.DriftResult
	for _, ref := range resources {
		drv, err := p.resourceDriver(ref.Type)
		if err != nil {
			continue
		}
		_, err = drv.Read(ctx, ref)
		if err != nil {
			results = append(results, interfaces.DriftResult{
				Name:    ref.Name,
				Type:    ref.Type,
				Drifted: true,
				Fields:  []string{"existence"},
			})
		} else {
			results = append(results, interfaces.DriftResult{
				Name:    ref.Name,
				Type:    ref.Type,
				Drifted: false,
			})
		}
	}
	return results, nil
}

// Import reads an existing AWS resource into a ResourceState.
func (p *AWSProvider) Import(ctx context.Context, cloudID string, resourceType string) (*interfaces.ResourceState, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}

	drv, err := p.resourceDriver(resourceType)
	if err != nil {
		return nil, err
	}
	out, err := drv.Read(ctx, interfaces.ResourceRef{Name: cloudID, Type: resourceType, ProviderID: cloudID})
	if err != nil {
		return nil, fmt.Errorf("aws: import %q: %w", cloudID, err)
	}

	return &interfaces.ResourceState{
		Name:       out.Name,
		Type:       out.Type,
		Provider:   ProviderName,
		ProviderID: out.ProviderID,
		Outputs:    out.Outputs,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

// ResolveSizing maps an abstract size tier to AWS-specific instance types.
func (p *AWSProvider) ResolveSizing(resourceType string, size interfaces.Size, hints *interfaces.ResourceHints) (*interfaces.ProviderSizing, error) {
	return resolveSizing(resourceType, size, hints)
}

func (p *AWSProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = false
	return nil
}

// resourceDriver is the internal (non-locking) lookup, called within locked sections.
func (p *AWSProvider) resourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	d, ok := p.driverMap[resourceType]
	if !ok {
		return nil, fmt.Errorf("aws: no driver for resource type %q", resourceType)
	}
	return d, nil
}

var _ interfaces.IaCProvider = (*AWSProvider)(nil)

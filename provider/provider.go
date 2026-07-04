// Package provider implements the AWS IaCProvider for workflow.
package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
	"github.com/GoCodeAlone/workflow/interfaces"
)

const (
	ProviderName = "aws"
)

// ProviderVersion is set at build time via -ldflags
// "-X github.com/GoCodeAlone/workflow-plugin-aws/provider.ProviderVersion=X.Y.Z".
// Declared as var (not const) so the linker can override it.
var ProviderVersion = "dev"

// AWSProvider implements interfaces.IaCProvider for Amazon Web Services.
type AWSProvider struct {
	mu          sync.RWMutex
	initialized bool
	region      string
	cfg         awssdk.Config
	driverMap   map[string]interfaces.ResourceDriver

	ownershipClient ownershipTaggingClient
	runnerClient    awsRunnerClient
	runnerConfig    awsRunnerConfig
}

// NewAWSProvider creates a new AWS provider.
func NewAWSProvider() interfaces.IaCProvider {
	return NewAWSProviderConcrete()
}

// NewAWSProviderConcrete creates a new *AWSProvider (concrete type).
// Used by internal.NewIaCServer to avoid a type assertion on the
// interfaces.IaCProvider return of NewAWSProvider.
func NewAWSProviderConcrete() *AWSProvider {
	return &AWSProvider{
		driverMap: make(map[string]interfaces.ResourceDriver),
	}
}

func (p *AWSProvider) Name() string    { return ProviderName }
func (p *AWSProvider) Version() string { return ProviderVersion }

// AWSConfigSnapshot returns the initialized AWS SDK config for typed services
// that need to call provider APIs outside the resource-driver path.
func (p *AWSProvider) AWSConfigSnapshot() (awssdk.Config, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg, p.initialized
}

// Initialize configures the AWS SDK and registers all resource drivers.
//
// Supported config keys (back-compat top-level):
//   - region
//   - access_key_id, secret_access_key
//   - ecs_cluster
//   - ecs_subnet_ids, ecs_security_group_ids
//   - ecs_task_execution_role_arn
//   - ecs_runner_log_group
//
// Supported config keys under the nested `credentials:` block (preferred
// shape — mirrors the standalone-module path from plan-2 Tasks 4-6):
//   - type        — "static" | "env" | "profile" | "role_arn"
//   - accessKey, secretKey, sessionToken
//   - profile     — shared-config profile name (honoured when type=="profile")
//   - roleArn, externalId, sessionName (honoured when type=="role_arn")
//
// Credential resolution flows through awscreds.BuildAWSConfig so the
// `credential_source` markers Phase B records on CloudAccount stay honoured
// in-plugin. CredInput.Source is populated from `credentials.type` (the YAML
// field), never from CloudAccount.Extra — which never crosses the gRPC
// boundary.
func (p *AWSProvider) Initialize(ctx context.Context, config map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	region, _ := config["region"].(string)
	if region == "" {
		region = "us-east-1"
	}
	p.region = region

	if providerMode(config) == "mock" {
		p.cfg = awssdk.Config{Region: region}
		p.ownershipClient = nil
		p.runnerClient = nil
		p.runnerConfig = awsRunnerConfig{}
		p.registerMockDrivers()
		p.initialized = true
		return nil
	}

	cred := awscreds.CredInput{Region: region}
	// Back-compat: top-level access_key_id / secret_access_key still honoured.
	cred.AccessKey, _ = config["access_key_id"].(string)
	cred.SecretKey, _ = config["secret_access_key"].(string)

	// Preferred: nested `credentials:` block. Values here override the
	// back-compat top-level keys when both are supplied.
	if credsMap, ok := config["credentials"].(map[string]any); ok {
		if v, _ := credsMap["type"].(string); v != "" {
			cred.Source = v
		}
		if v, _ := credsMap["accessKey"].(string); v != "" {
			cred.AccessKey = v
		}
		if v, _ := credsMap["secretKey"].(string); v != "" {
			cred.SecretKey = v
		}
		if v, _ := credsMap["sessionToken"].(string); v != "" {
			cred.SessionToken = v
		}
		if v, _ := credsMap["profile"].(string); v != "" {
			cred.Profile = v
		}
		if v, _ := credsMap["roleArn"].(string); v != "" {
			cred.RoleARN = v
		}
		if v, _ := credsMap["externalId"].(string); v != "" {
			cred.ExternalID = v
		}
		if v, _ := credsMap["sessionName"].(string); v != "" {
			cred.SessionName = v
		}
	}

	cfg, err := awscreds.BuildAWSConfig(ctx, cred)
	if err != nil {
		return fmt.Errorf("aws: load config: %w", err)
	}
	p.cfg = cfg
	p.ownershipClient = resourcegroupstaggingapi.NewFromConfig(cfg)

	ecsCluster, _ := config["ecs_cluster"].(string)
	if ecsCluster == "" {
		ecsCluster = "default"
	}
	p.runnerConfig = awsRunnerConfig{
		cluster:              ecsCluster,
		region:               region,
		subnetIDs:            stringSliceConfig(config["ecs_subnet_ids"]),
		securityGroupIDs:     stringSliceConfig(config["ecs_security_group_ids"]),
		taskExecutionRoleARN: stringConfig(config["ecs_task_execution_role_arn"]),
		logGroup:             stringConfig(config["ecs_runner_log_group"]),
	}
	if p.runnerConfig.logGroup == "" {
		p.runnerConfig.logGroup = "/workflow/provider-ephemeral"
	}
	p.runnerClient = newRealAWSRunnerClient(cfg)
	p.registerDrivers(cfg, ecsCluster, region)
	p.initialized = true
	return nil
}

func providerMode(config map[string]any) string {
	if mode, _ := config["mode"].(string); mode != "" {
		return strings.ToLower(strings.TrimSpace(mode))
	}
	if mock, _ := config["mock"].(bool); mock {
		return "mock"
	}
	if credsMap, ok := config["credentials"].(map[string]any); ok {
		if mode, _ := credsMap["type"].(string); mode != "" {
			return strings.ToLower(strings.TrimSpace(mode))
		}
	}
	return ""
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
		drivers.NewAutoScalingGroupDriver(cfg),
	}
	for _, d := range driverList {
		p.driverMap[d.ResourceType()] = d
	}
}

func (p *AWSProvider) registerMockDrivers() {
	clear(p.driverMap)
	for _, cap := range p.Capabilities() {
		p.driverMap[cap.ResourceType] = newMockResourceDriver(cap.ResourceType)
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
		{ResourceType: "infra.autoscaling_group", Tier: 2, Operations: []string{"create", "read", "update", "delete"}},
	}
}

func (p *AWSProvider) ResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.resourceDriver(resourceType)
}

func stringConfig(raw any) string {
	v, _ := raw.(string)
	return v
}

func stringSliceConfig(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(values, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	default:
		return nil
	}
}

// Plan computes required create/update/delete actions for the desired state.
func (p *AWSProvider) Plan(ctx context.Context, desired []interfaces.ResourceSpec, current []interfaces.ResourceState) (*interfaces.IaCPlan, error) {
	if err := p.ensureInitialized(); err != nil {
		return nil, err
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

		drv, err := p.initializedResourceDriver(spec.Type)
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

// Destroy deletes a set of resources.
func (p *AWSProvider) Destroy(ctx context.Context, resources []interfaces.ResourceRef) (*interfaces.DestroyResult, error) {
	result := &interfaces.DestroyResult{}
	for _, ref := range resources {
		drv, err := p.initializedResourceDriver(ref.Type)
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
	var statuses []interfaces.ResourceStatus
	for _, ref := range resources {
		drv, err := p.initializedResourceDriver(ref.Type)
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
	var results []interfaces.DriftResult
	for _, ref := range resources {
		drv, err := p.initializedResourceDriver(ref.Type)
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
	drv, err := p.initializedResourceDriver(resourceType)
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

// SupportedCanonicalKeys returns the full canonical IaC key set plus the
// AWS-specific keys accepted by this provider.
func (p *AWSProvider) SupportedCanonicalKeys() []string {
	canonical := interfaces.CanonicalKeys()
	awsSpecific := []string{
		"access_key_id",
		"secret_access_key",
		"mode",
		"ecs_cluster",
		"ecs_subnet_ids",
		"ecs_security_group_ids",
		"ecs_task_execution_role_arn",
		"ecs_runner_log_group",
	}
	result := make([]string, 0, len(canonical)+len(awsSpecific))
	result = append(result, canonical...)
	result = append(result, awsSpecific...)
	return result
}

// BootstrapStateBackend is a no-op for this provider; AWS S3 state backends
// are managed via separate workflow paths rather than the provider interface.
// Returns (nil, nil) per interfaces.IaCProvider's documented contract for
// providers that do not manage state.
func (p *AWSProvider) BootstrapStateBackend(_ context.Context, _ map[string]any) (*interfaces.BootstrapResult, error) {
	return nil, nil
}

func (p *AWSProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = false
	return nil
}

func (p *AWSProvider) ensureInitialized() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return fmt.Errorf("aws: provider not initialized")
	}
	return nil
}

func (p *AWSProvider) initializedResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}
	return p.resourceDriver(resourceType)
}

// resourceDriver is the internal lookup, called within locked sections.
func (p *AWSProvider) resourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	d, ok := p.driverMap[resourceType]
	if !ok {
		return nil, fmt.Errorf("aws: no driver for resource type %q", resourceType)
	}
	return d, nil
}

var _ interfaces.IaCProvider = (*AWSProvider)(nil)

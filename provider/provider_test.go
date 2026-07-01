package provider_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-aws/provider"
	"github.com/GoCodeAlone/workflow/interfaces"
)

func TestNewAWSProvider(t *testing.T) {
	p := provider.NewAWSProvider()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "aws" {
		t.Errorf("expected name aws, got %s", p.Name())
	}
	// Version is set at link time via -ldflags; in unit tests the default
	// "dev" sentinel applies. The release pipeline overrides it to the tag
	// (workflow#758 Layer 3 — see provider.ProviderVersion).
	if p.Version() != provider.ProviderVersion {
		t.Errorf("expected version %s, got %s", provider.ProviderVersion, p.Version())
	}
	if p.Version() == "" {
		t.Error("expected non-empty version")
	}
}

func TestAWSProvider_Capabilities(t *testing.T) {
	p := provider.NewAWSProvider()
	caps := p.Capabilities()
	if len(caps) != 14 {
		t.Errorf("expected 14 capabilities, got %d", len(caps))
	}

	// Verify all required resource types are present
	required := []string{
		"infra.container_service",
		"infra.k8s_cluster",
		"infra.database",
		"infra.cache",
		"infra.vpc",
		"infra.load_balancer",
		"infra.dns",
		"infra.registry",
		"infra.api_gateway",
		"infra.firewall",
		"infra.iam_role",
		"infra.storage",
		"infra.certificate",
		"infra.autoscaling_group",
	}
	capSet := make(map[string]bool)
	for _, c := range caps {
		capSet[c.ResourceType] = true
	}
	for _, rt := range required {
		if !capSet[rt] {
			t.Errorf("missing capability: %s", rt)
		}
	}
}

func TestAWSProvider_ResolveSizing(t *testing.T) {
	p := provider.NewAWSProvider()

	tests := []struct {
		resourceType string
		size         interfaces.Size
		expectType   string
	}{
		{"infra.database", interfaces.SizeXS, "db.t3.micro"},
		{"infra.database", interfaces.SizeL, "db.r6g.2xlarge"},
		{"infra.cache", interfaces.SizeXS, "cache.t3.micro"},
		{"infra.cache", interfaces.SizeL, "cache.r6g.2xlarge"},
		{"infra.container_service", interfaces.SizeXS, "t3.micro"},
		{"infra.container_service", interfaces.SizeXL, "m5.4xlarge"},
	}

	for _, tt := range tests {
		sizing, err := p.ResolveSizing(tt.resourceType, tt.size, nil)
		if err != nil {
			t.Errorf("ResolveSizing(%s, %s): %v", tt.resourceType, tt.size, err)
			continue
		}
		if sizing.InstanceType != tt.expectType {
			t.Errorf("ResolveSizing(%s, %s): expected %s, got %s",
				tt.resourceType, tt.size, tt.expectType, sizing.InstanceType)
		}
	}
}

func TestAWSProvider_ResourceDriver_NotInitialized(t *testing.T) {
	p := provider.NewAWSProvider()
	_, err := p.ResourceDriver("infra.database")
	if err == nil {
		t.Fatal("expected error from ResourceDriver before Initialize")
	}
	if !strings.Contains(err.Error(), "no driver for resource type") {
		t.Errorf("expected 'no driver for resource type' error, got: %v", err)
	}
}

func TestAWSProvider_ResourceDriver_UnknownType(t *testing.T) {
	p := provider.NewAWSProvider()
	_, err := p.ResourceDriver("infra.nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown resource type")
	}
	if !strings.Contains(err.Error(), "no driver for resource type") {
		t.Errorf("expected 'no driver for resource type' error, got: %v", err)
	}
}

func TestAWSProvider_MockModeProvidesInMemoryContainerServiceDriver(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	p := provider.NewAWSProviderConcrete()
	if err := p.Initialize(ctx, map[string]any{
		"mode":   "mock",
		"region": "us-east-1",
	}); err != nil {
		t.Fatalf("Initialize mock provider: %v", err)
	}

	driver, err := p.ResourceDriver("infra.container_service")
	if err != nil {
		t.Fatalf("ResourceDriver infra.container_service: %v", err)
	}

	out, err := driver.Create(ctx, interfaces.ResourceSpec{
		Name: "staging-ecs",
		Type: "infra.container_service",
		Config: map[string]any{
			"image":    "public.ecr.aws/nginx/nginx:latest",
			"replicas": 2,
			"cluster":  "staging-cluster",
			"region":   "us-east-1",
		},
	})
	if err != nil {
		t.Fatalf("Create mock container service: %v", err)
	}
	if out.ProviderID == "" {
		t.Fatal("Create mock container service returned empty provider id")
	}
	if out.Status != "running" {
		t.Fatalf("Create status = %q, want running", out.Status)
	}
	if out.Outputs["image"] != "public.ecr.aws/nginx/nginx:latest" {
		t.Fatalf("Create image output = %v", out.Outputs["image"])
	}
	if out.Outputs["replicas"] != 2 {
		t.Fatalf("Create replicas output = %v", out.Outputs["replicas"])
	}

	read, err := driver.Read(ctx, interfaces.ResourceRef{Name: "staging-ecs", Type: "infra.container_service"})
	if err != nil {
		t.Fatalf("Read mock container service: %v", err)
	}
	if read.ProviderID != out.ProviderID {
		t.Fatalf("Read provider id = %q, want %q", read.ProviderID, out.ProviderID)
	}

	if err := driver.Delete(ctx, interfaces.ResourceRef{Name: "staging-ecs", Type: "infra.container_service"}); err != nil {
		t.Fatalf("Delete mock container service: %v", err)
	}
	if _, err := driver.Read(ctx, interfaces.ResourceRef{Name: "staging-ecs", Type: "infra.container_service"}); err == nil {
		t.Fatal("Read after delete succeeded; want not found error")
	}
}

func TestAWSProvider_MockModeUpdateUsesRefIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	p := provider.NewAWSProviderConcrete()
	if err := p.Initialize(ctx, map[string]any{"mode": "mock", "region": "us-east-1"}); err != nil {
		t.Fatalf("Initialize mock provider: %v", err)
	}
	driver, err := p.ResourceDriver("infra.container_service")
	if err != nil {
		t.Fatalf("ResourceDriver infra.container_service: %v", err)
	}

	if _, err := driver.Create(ctx, interfaces.ResourceSpec{
		Name:   "staging-ecs",
		Type:   "infra.container_service",
		Config: map[string]any{"image": "old", "replicas": 1},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := driver.Update(ctx, interfaces.ResourceRef{Name: "staging-ecs", Type: "infra.container_service"}, interfaces.ResourceSpec{
		Name:   "",
		Type:   "infra.container_service",
		Config: map[string]any{"image": "new", "replicas": 3},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "staging-ecs" {
		t.Fatalf("Update name = %q, want ref name", updated.Name)
	}
	if updated.ProviderID != "mock-aws:infra.container_service:staging-ecs" {
		t.Fatalf("Update provider id = %q", updated.ProviderID)
	}

	read, err := driver.Read(ctx, interfaces.ResourceRef{Name: "staging-ecs", Type: "infra.container_service"})
	if err != nil {
		t.Fatalf("Read after update: %v", err)
	}
	if read.ProviderID != updated.ProviderID {
		t.Fatalf("Read provider id = %q, want %q", read.ProviderID, updated.ProviderID)
	}
}

func TestAWSProvider_MockModeDiffHandlesNestedConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	p := provider.NewAWSProviderConcrete()
	if err := p.Initialize(ctx, map[string]any{"mode": "mock", "region": "us-east-1"}); err != nil {
		t.Fatalf("Initialize mock provider: %v", err)
	}
	driver, err := p.ResourceDriver("infra.container_service")
	if err != nil {
		t.Fatalf("ResourceDriver infra.container_service: %v", err)
	}

	diff, err := driver.Diff(ctx, interfaces.ResourceSpec{
		Name: "staging-ecs",
		Type: "infra.container_service",
		Config: map[string]any{
			"env": map[string]any{"APP_ENV": "staging"},
		},
	}, &interfaces.ResourceOutput{
		Name: "staging-ecs",
		Type: "infra.container_service",
		Outputs: map[string]any{
			"env": map[string]any{"APP_ENV": "prod"},
		},
	})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == nil || !diff.NeedsUpdate {
		t.Fatalf("Diff = %#v, want update for nested config change", diff)
	}
}

func TestAWSProvider_SupportedCanonicalKeys(t *testing.T) {
	p := provider.NewAWSProvider()
	keys := p.SupportedCanonicalKeys()
	if len(keys) == 0 {
		t.Fatal("expected at least one canonical key")
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	// Must include the full canonical key set.
	for _, required := range interfaces.CanonicalKeys() {
		if !keySet[required] {
			t.Errorf("SupportedCanonicalKeys missing canonical key %q", required)
		}
	}
	// Must also include the AWS-specific credential, cluster, and runner keys.
	for _, required := range []string{
		"access_key_id",
		"secret_access_key",
		"ecs_cluster",
		"ecs_subnet_ids",
		"ecs_security_group_ids",
		"ecs_task_execution_role_arn",
		"ecs_runner_log_group",
	} {
		if !keySet[required] {
			t.Errorf("SupportedCanonicalKeys missing AWS-specific key %q", required)
		}
	}
}

func TestAWSProvider_BootstrapStateBackend(t *testing.T) {
	p := provider.NewAWSProvider()
	result, err := p.BootstrapStateBackend(context.Background(), nil)
	if err != nil {
		t.Fatalf("BootstrapStateBackend: unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("BootstrapStateBackend: expected nil result for no-op provider, got %v", result)
	}
}

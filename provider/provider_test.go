package provider_test

import (
	"strings"
	"testing"

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
	if p.Version() != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", p.Version())
	}
}

func TestAWSProvider_Capabilities(t *testing.T) {
	p := provider.NewAWSProvider()
	caps := p.Capabilities()
	if len(caps) != 13 {
		t.Errorf("expected 13 capabilities, got %d", len(caps))
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

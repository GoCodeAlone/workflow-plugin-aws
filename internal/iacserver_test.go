// Package internal_test exercises the awsIaCServer typed gRPC methods.
// Tests use a real *provider.AWSProvider with no initialized AWS session;
// only methods that do NOT require a live AWS credential are covered here.
// Initialize, Plan, Apply, Destroy, Import, Status test coverage lives in
// provider/provider_test.go (existing suite).
package internal

import (
	"context"
	"testing"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

func TestNewIaCServer_NotNil(t *testing.T) {
	s := NewIaCServer()
	if s == nil {
		t.Fatal("NewIaCServer returned nil")
	}
}

func TestIaCServer_Name(t *testing.T) {
	s := NewIaCServer()
	resp, err := s.Name(context.Background(), &pb.NameRequest{})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if resp.GetName() != "aws" {
		t.Errorf("Name = %q, want %q", resp.GetName(), "aws")
	}
}

func TestIaCServer_Version(t *testing.T) {
	s := NewIaCServer()
	resp, err := s.Version(context.Background(), &pb.VersionRequest{})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if resp.GetVersion() == "" {
		t.Error("Version returned empty string")
	}
}

func TestIaCServer_Capabilities(t *testing.T) {
	s := NewIaCServer()
	resp, err := s.Capabilities(context.Background(), &pb.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	found := false
	for _, c := range resp.GetCapabilities() {
		if c.GetResourceType() == "infra.container_service" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Capabilities missing infra.container_service, got: %v", resp.GetCapabilities())
	}
}

func TestIaCServer_Initialize_EmptyConfig(t *testing.T) {
	s := NewIaCServer()
	// Empty config_json: Initialize should return an error (no region defaults to us-east-1,
	// but nil map should succeed since AWSProvider.Initialize handles nil gracefully).
	_, err := s.Initialize(context.Background(), &pb.InitializeRequest{ConfigJson: []byte(`{}`)})
	// No credential required for unit test — Initialize sets up the SDK config.
	// In CI without AWS credentials, LoadDefaultConfig may still succeed with the ambient chain.
	// We only assert it does not panic.
	_ = err // error acceptable; not nil is fine without credentials
}

func TestIaCServer_CompileTimeGuards(t *testing.T) {
	// This test exists to document the compile-time guards.
	// If any of the interface assertions below fail to compile, this file will not build.
	var _ pb.IaCProviderRequiredServer = (*awsIaCServer)(nil)
	var _ pb.IaCProviderDriftDetectorServer = (*awsIaCServer)(nil)
	var _ pb.ResourceDriverServer = (*awsIaCServer)(nil)
}

func TestIaCServer_DetectDrift_Uninitialized(t *testing.T) {
	s := NewIaCServer()
	refs := []*pb.ResourceRef{{Name: "test", Type: "infra.container_service"}}
	_, err := s.DetectDrift(context.Background(), &pb.DetectDriftRequest{Refs: refs})
	// Uninitialized provider returns "not initialized" error.
	if err == nil {
		t.Error("expected error from uninitialized provider")
	}
}

func TestIaCServer_DetectDriftWithSpecs_DelegatesToDetectDrift(t *testing.T) {
	s := NewIaCServer()
	refs := []*pb.ResourceRef{{Name: "test", Type: "infra.container_service"}}
	_, err := s.DetectDriftWithSpecs(context.Background(), &pb.DetectDriftWithSpecsRequest{Refs: refs})
	// Uninitialized provider returns "not initialized" error — same as DetectDrift.
	if err == nil {
		t.Error("expected error from uninitialized provider")
	}
}

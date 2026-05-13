package internal

import (
	"context"
	"testing"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

func TestResourceDriverServer_CompileTimeGuard(t *testing.T) {
	var _ pb.ResourceDriverServer = (*awsIaCServer)(nil)
}

func TestResourceDriverServer_ResolveDriver_Empty(t *testing.T) {
	s := NewIaCServer()
	_, err := s.resolveResourceDriver("")
	if err == nil {
		t.Fatal("expected error for empty resource_type")
	}
}

func TestResourceDriverServer_ResolveDriver_Unknown(t *testing.T) {
	s := NewIaCServer()
	// Provider not initialized — resolveResourceDriver returns error.
	_, err := s.resolveResourceDriver("infra.unknown_type")
	if err == nil {
		t.Fatal("expected error for unknown resource type on uninitialized provider")
	}
}

func TestResourceDriverServer_Create_UnknownType(t *testing.T) {
	s := NewIaCServer()
	req := &pb.ResourceCreateRequest{
		ResourceType: "infra.unknown",
		Spec:         &pb.ResourceSpec{Name: "x", Type: "infra.unknown"},
	}
	_, err := s.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unknown resource type")
	}
}

func TestResourceDriverServer_SensitiveKeys_UnknownType(t *testing.T) {
	s := NewIaCServer()
	_, err := s.SensitiveKeys(context.Background(), &pb.SensitiveKeysRequest{ResourceType: "infra.unknown"})
	if err == nil {
		t.Fatal("expected error for unknown resource type")
	}
}

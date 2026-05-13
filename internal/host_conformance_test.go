package internal

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow/plugin/external"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

// TestWorkflowHostConformance_LoadsTypedIaCPlugin validates the host/plugin
// boundary for the typed-IaC gRPC pattern (sdk.ServeIaCPlugin). Skipped by
// default; set WORKFLOW_IAC_HOST_CONFORMANCE=1 to run.
//
// This test mirrors workflow-plugin-digitalocean v1.0.1
// internal/host_conformance_test.go exactly.
func TestWorkflowHostConformance_LoadsTypedIaCPlugin(t *testing.T) {
	if os.Getenv("WORKFLOW_IAC_HOST_CONFORMANCE") != "1" {
		t.Skip("set WORKFLOW_IAC_HOST_CONFORMANCE=1 to run host compatibility smoke")
	}

	repoRoot := hostConformanceRepoRoot(t)
	pluginName := hostConformancePluginName(t, filepath.Join(repoRoot, "plugin.json"))

	pluginsDir := filepath.Join(t.TempDir(), "data", "plugins")
	pluginDir := filepath.Join(pluginsDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	hostConformanceCopyFile(t, filepath.Join(repoRoot, "plugin.json"), filepath.Join(pluginDir, "plugin.json"))
	hostConformanceCopyFile(t, filepath.Join(repoRoot, "plugin.contracts.json"), filepath.Join(pluginDir, "plugin.contracts.json"))

	build := exec.Command("go", "build", "-o", filepath.Join(pluginDir, pluginName), "./cmd/workflow-plugin-aws")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin binary: %v\n%s", err, out)
	}

	mgr := external.NewExternalPluginManager(pluginsDir, nil)
	t.Cleanup(mgr.Shutdown)

	adapter, err := mgr.LoadPlugin(pluginName)
	if err != nil {
		t.Fatalf("load plugin through Workflow external host: %v", err)
	}

	registry := adapter.ContractRegistry()
	if registry == nil {
		t.Fatal("contract registry is nil")
	}
	// Typed-IaC plugins expose SERVICE-kind contracts (not module-kind).
	if !registryHasService(registry, pb.IaCProviderRequired_ServiceDesc.ServiceName) {
		t.Fatalf("contract registry missing required service %q: %v",
			pb.IaCProviderRequired_ServiceDesc.ServiceName, registry.GetContracts())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	required := pb.NewIaCProviderRequiredClient(adapter.Conn())
	name, err := required.Name(ctx, &pb.NameRequest{})
	if err != nil {
		t.Fatalf("call typed IaCProviderRequired.Name: %v", err)
	}
	if name.GetName() != "aws" {
		t.Fatalf("provider name = %q, want %q", name.GetName(), "aws")
	}

	capabilities, err := required.Capabilities(ctx, &pb.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("call typed IaCProviderRequired.Capabilities: %v", err)
	}
	if !capabilitiesHasResource(capabilities, "infra.container_service") {
		t.Fatalf("provider capabilities missing infra.container_service: %v",
			capabilities.GetCapabilities())
	}
}

func hostConformanceRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func hostConformancePluginName(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plugin manifest: %v", err)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin manifest: %v", err)
	}
	if manifest.Name == "" {
		t.Fatal("plugin manifest missing name")
	}
	return manifest.Name
}

func hostConformanceCopyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func registryHasService(registry *pb.ContractRegistry, serviceName string) bool {
	for _, contract := range registry.GetContracts() {
		if contract.GetKind() == pb.ContractKind_CONTRACT_KIND_SERVICE &&
			contract.GetServiceName() == serviceName {
			return true
		}
	}
	return false
}

func capabilitiesHasResource(capabilities *pb.CapabilitiesResponse, resourceType string) bool {
	for _, capability := range capabilities.GetCapabilities() {
		if capability.GetResourceType() == resourceType {
			return true
		}
	}
	return false
}

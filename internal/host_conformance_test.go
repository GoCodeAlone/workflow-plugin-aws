package internal

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
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

func TestEmbeddedManifestIaCServicesMatchRootManifest(t *testing.T) {
	repoRoot := hostConformanceRepoRoot(t)
	rootServices := readManifestIaCServices(t, filepath.Join(repoRoot, "plugin.json"))
	embeddedServices := readManifestIaCServices(t, filepath.Join(repoRoot, "cmd", "workflow-plugin-aws", "plugin.json"))
	sort.Strings(rootServices)
	sort.Strings(embeddedServices)
	if !reflect.DeepEqual(rootServices, embeddedServices) {
		t.Fatalf("embedded manifest iacServices = %v, want root manifest services %v", embeddedServices, rootServices)
	}
}

func readManifestIaCServices(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var manifest struct {
		IaCServices []string `json:"iacServices"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return append([]string(nil), manifest.IaCServices...)
}

// TestCapabilityParity_IaCStateBackends asserts that every iac.state backend
// name declared in plugin.json capabilities.iacStateBackends is actually
// served by the plugin — i.e. returned by NewIaCServer().ListBackendNames.
// This guards against a manifest claiming a backend the plugin does not serve.
func TestCapabilityParity_IaCStateBackends(t *testing.T) {
	repoRoot := hostConformanceRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, "plugin.json"))
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	var manifest struct {
		Capabilities struct {
			IaCStateBackends []string `json:"iacStateBackends"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}

	resp, err := NewIaCServer().ListBackendNames(context.Background(), &pb.ListBackendNamesRequest{})
	if err != nil {
		t.Fatalf("ListBackendNames: %v", err)
	}
	served := make(map[string]bool, len(resp.GetBackendNames()))
	for _, n := range resp.GetBackendNames() {
		served[n] = true
	}

	for _, declared := range manifest.Capabilities.IaCStateBackends {
		if !served[declared] {
			t.Errorf("plugin.json declares iacStateBackends entry %q but ListBackendNames does not serve it (served: %v)",
				declared, resp.GetBackendNames())
		}
	}
}

// TestPluginJSONCapabilities_ModuleStep_Parity asserts that the type-name
// keys in the providers wired into IaCServeOptions (ModuleProviders /
// StepProviders) exactly match plugin.json's capabilities.moduleTypes /
// capabilities.stepTypes (modulo the always-implicit "iac.provider" module
// type, which is served via the IaC contract surface and is NOT a
// standalone-module provider).
//
// This is the in-process equivalent of the gRPC bridge's GetModuleTypes /
// GetStepTypes RPCs (the bridge surfaces ModuleProviders' keys verbatim via
// plan-2 PR 1's mapBackedProvider adapter), and catches drift between
// "what main.go wires" and "what plugin.json declares".
func TestPluginJSONCapabilities_ModuleStep_Parity(t *testing.T) {
	repoRoot := hostConformanceRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, "plugin.json"))
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	var manifest struct {
		Capabilities struct {
			ModuleTypes []string `json:"moduleTypes"`
			StepTypes   []string `json:"stepTypes"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}

	// Module-type parity. plugin.json includes the implicit "iac.provider"
	// (served via the IaC contract, NOT via ModuleProviders); filter it out
	// before comparing.
	declaredModules := map[string]bool{}
	for _, m := range manifest.Capabilities.ModuleTypes {
		if m == "iac.provider" {
			continue
		}
		declaredModules[m] = true
	}
	servedModules := map[string]bool{}
	for name := range ModuleProviders() {
		servedModules[name] = true
	}
	for m := range declaredModules {
		if !servedModules[m] {
			t.Errorf("plugin.json declares moduleTypes entry %q but ModuleProviders does not serve it (served: %v)",
				m, servedModules)
		}
	}
	for m := range servedModules {
		if !declaredModules[m] {
			t.Errorf("ModuleProviders serves %q but plugin.json capabilities.moduleTypes does not declare it (declared: %v)",
				m, declaredModules)
		}
	}

	// Step-type parity.
	declaredSteps := map[string]bool{}
	for _, s := range manifest.Capabilities.StepTypes {
		declaredSteps[s] = true
	}
	servedSteps := map[string]bool{}
	for name := range StepProviders() {
		servedSteps[name] = true
	}
	for s := range declaredSteps {
		if !servedSteps[s] {
			t.Errorf("plugin.json declares stepTypes entry %q but StepProviders does not serve it (served: %v)",
				s, servedSteps)
		}
	}
	for s := range servedSteps {
		if !declaredSteps[s] {
			t.Errorf("StepProviders serves %q but plugin.json capabilities.stepTypes does not declare it (declared: %v)",
				s, declaredSteps)
		}
	}
}

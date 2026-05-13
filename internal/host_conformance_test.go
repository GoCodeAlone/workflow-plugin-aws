package internal

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GoCodeAlone/workflow/plugin/external"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

func TestWorkflowHostConformance_LoadsLegacyIaCModulePlugin(t *testing.T) {
	if os.Getenv("WORKFLOW_IAC_HOST_CONFORMANCE") != "1" {
		t.Skip("set WORKFLOW_IAC_HOST_CONFORMANCE=1 to run host compatibility smoke")
	}

	// AWS is still a legacy sdk.Serve module plugin, not a strict-cutover
	// sdk.ServeIaCPlugin provider. This gate validates the host/plugin boundary
	// it actually ships today: external plugin load, iac.provider discovery, and
	// strict module contract registry exposure across engine versions.
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
	if adapter.Name() != pluginName {
		t.Fatalf("host adapter name = %q, want %q", adapter.Name(), pluginName)
	}
	if !hasString(adapter.EngineManifest().ModuleTypes, moduleTypeIaCProvider) {
		t.Fatalf("host adapter module types = %v, want %q", adapter.EngineManifest().ModuleTypes, moduleTypeIaCProvider)
	}

	registry := adapter.ContractRegistry()
	if registry == nil {
		t.Fatal("contract registry is nil")
	}
	if !registryHasModule(registry, moduleTypeIaCProvider) {
		t.Fatalf("contract registry missing module %q: %v", moduleTypeIaCProvider, registry.GetContracts())
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

func registryHasModule(registry *pb.ContractRegistry, moduleType string) bool {
	for _, contract := range registry.GetContracts() {
		if contract.GetKind() == pb.ContractKind_CONTRACT_KIND_MODULE && contract.GetModuleType() == moduleType {
			return true
		}
	}
	return false
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

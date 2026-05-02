package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/contracts"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestAWSPluginImplementsStrictContractProviders(t *testing.T) {
	provider := NewAWSPlugin()
	if _, ok := provider.(sdk.TypedModuleProvider); !ok {
		t.Fatal("expected TypedModuleProvider")
	}
	if _, ok := provider.(sdk.ContractProvider); !ok {
		t.Fatal("expected ContractProvider")
	}
}

func TestContractRegistryDeclaresStrictModuleContracts(t *testing.T) {
	provider := NewAWSPlugin().(sdk.ContractProvider)
	registry := provider.ContractRegistry()
	if registry == nil {
		t.Fatal("expected contract registry")
	}
	if registry.FileDescriptorSet == nil || len(registry.FileDescriptorSet.File) == 0 {
		t.Fatal("expected file descriptor set")
	}
	files, err := protodesc.NewFiles(registry.FileDescriptorSet)
	if err != nil {
		t.Fatalf("descriptor set: %v", err)
	}

	manifestContracts := loadManifestContracts(t)
	contractsByKey := map[string]*pb.ContractDescriptor{}
	for _, contract := range registry.Contracts {
		if contract.Kind != pb.ContractKind_CONTRACT_KIND_MODULE {
			t.Fatalf("unexpected contract kind %s", contract.Kind)
		}
		key := "module:" + contract.ModuleType
		contractsByKey[key] = contract
		if contract.Mode != pb.ContractMode_CONTRACT_MODE_STRICT_PROTO {
			t.Fatalf("%s mode = %s, want strict proto", key, contract.Mode)
		}
		if contract.ConfigMessage == "" {
			t.Fatalf("%s missing config message", key)
		}
		if _, err := files.FindDescriptorByName(protoreflect.FullName(contract.ConfigMessage)); err != nil {
			t.Fatalf("%s references unknown config message %s: %v", key, contract.ConfigMessage, err)
		}
		if want, ok := manifestContracts[key]; !ok {
			t.Fatalf("%s missing from plugin.contracts.json", key)
		} else if want.ConfigMessage != contract.ConfigMessage {
			t.Fatalf("%s manifest contract = %#v, runtime = %#v", key, want, contract)
		}
	}

	for _, moduleType := range pluginTypedModuleTypes() {
		key := "module:" + moduleType
		if _, ok := contractsByKey[key]; !ok {
			t.Fatalf("missing contract %s", key)
		}
	}
	if len(manifestContracts) != len(contractsByKey) {
		t.Fatalf("plugin.contracts.json contract count = %d, runtime = %d", len(manifestContracts), len(contractsByKey))
	}
}

func TestTypedModuleProviderValidatesTypedConfig(t *testing.T) {
	provider := NewAWSPlugin().(sdk.TypedModuleProvider)
	config, err := anypb.New(&contracts.AWSProviderConfig{
		Region:     "us-east-1",
		EcsCluster: "my-cluster",
	})
	if err != nil {
		t.Fatalf("pack config: %v", err)
	}
	module, err := provider.CreateTypedModule("iac.provider", "aws", config)
	if err != nil {
		t.Fatalf("CreateTypedModule: %v", err)
	}
	if module == nil {
		t.Fatal("expected non-nil module")
	}
}

func TestTypedModuleProviderRejectsWrongType(t *testing.T) {
	provider := NewAWSPlugin().(sdk.TypedModuleProvider)
	config, err := anypb.New(&contracts.AWSProviderConfig{Region: "us-east-1"})
	if err != nil {
		t.Fatalf("pack config: %v", err)
	}
	// Reject unknown module type name.
	if _, err := provider.CreateTypedModule("iac.unknown", "x", config); err == nil {
		t.Fatal("CreateTypedModule accepted unknown module type")
	}

	// Reject correct module type but wrong proto message payload.
	wrongConfig, err := anypb.New(wrapperspb.String("bad-payload"))
	if err != nil {
		t.Fatalf("pack wrong config: %v", err)
	}
	if _, err := provider.CreateTypedModule("iac.provider", "x", wrongConfig); err == nil {
		t.Fatal("CreateTypedModule accepted wrong proto message type for iac.provider")
	}
}

func TestTypedModuleProviderConfigMapsToLegacyModule(t *testing.T) {
	provider := NewAWSPlugin().(sdk.TypedModuleProvider)
	config, err := anypb.New(&contracts.AWSProviderConfig{
		Region:          "eu-west-1",
		AccessKeyId:     "AKID",
		SecretAccessKey: "SECRET",
		EcsCluster:      "prod",
	})
	if err != nil {
		t.Fatalf("pack config: %v", err)
	}
	module, err := provider.CreateTypedModule("iac.provider", "aws", config)
	if err != nil {
		t.Fatalf("CreateTypedModule: %v", err)
	}
	wrapped, ok := module.(*sdk.TypedModuleInstance[*contracts.AWSProviderConfig])
	if !ok {
		t.Fatalf("module type = %T, want *sdk.TypedModuleInstance[*contracts.AWSProviderConfig]", module)
	}
	legacy, ok := wrapped.ModuleInstance.(*iacProviderModule)
	if !ok {
		t.Fatalf("wrapped module type = %T, want *iacProviderModule", wrapped.ModuleInstance)
	}
	if got := legacy.config["region"]; got != "eu-west-1" {
		t.Fatalf("region = %q, want eu-west-1", got)
	}
	if got := legacy.config["access_key_id"]; got != "AKID" {
		t.Fatalf("access_key_id = %q, want AKID", got)
	}
	if got := legacy.config["ecs_cluster"]; got != "prod" {
		t.Fatalf("ecs_cluster = %q, want prod", got)
	}
	if got := legacy.config["secret_access_key"]; got != "SECRET" {
		t.Fatalf("secret_access_key = %q, want SECRET", got)
	}
}

// pluginTypedModuleTypes calls TypedModuleTypes() on a fresh plugin instance.
// It is called lazily within tests rather than at package init to avoid side
// effects during test binary loading.
func pluginTypedModuleTypes() []string {
	return NewAWSPlugin().(sdk.TypedModuleProvider).TypedModuleTypes()
}

type manifestContract struct {
	Mode          string `json:"mode"`
	ConfigMessage string `json:"config"`
}

func loadManifestContracts(t *testing.T) map[string]manifestContract {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "plugin.contracts.json"))
	if err != nil {
		t.Fatalf("read plugin.contracts.json: %v", err)
	}
	var manifest struct {
		Version   string `json:"version"`
		Contracts []struct {
			Kind string `json:"kind"`
			Type string `json:"type"`
			manifestContract
		} `json:"contracts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.contracts.json: %v", err)
	}
	if manifest.Version != "v1" {
		t.Fatalf("plugin.contracts.json version = %q, want v1", manifest.Version)
	}
	result := make(map[string]manifestContract, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		if contract.Kind != "module" {
			t.Fatalf("unexpected contract kind %q in plugin.contracts.json", contract.Kind)
		}
		if contract.Mode != "strict" {
			t.Fatalf("%s mode = %q, want strict", contract.Type, contract.Mode)
		}
		key := "module:" + contract.Type
		if _, exists := result[key]; exists {
			t.Fatalf("duplicate contract %q in plugin.contracts.json", key)
		}
		result[key] = contract.manifestContract
	}
	return result
}

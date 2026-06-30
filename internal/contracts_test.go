package internal

import (
	"testing"

	core "github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
)

func TestS3ContentSourceProviderContract(t *testing.T) {
	contract := S3ContentSourceProviderContract()
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if contract.PluginID != "workflow-plugin-aws" || contract.ProviderID != "s3-content-source" {
		t.Fatalf("provider identity = %s/%s, want workflow-plugin-aws/s3-content-source",
			contract.PluginID, contract.ProviderID)
	}
	if !contract.SupportsOperation("s3_fetch") {
		t.Fatalf("contract does not expose s3_fetch operation: %#v", contract.Operations)
	}
	op := contract.Operations[0]
	specs := op.NormalizedArtifactSpecs()
	if len(specs) != 1 || specs[0].Name != "content" || !specs[0].Forwardable {
		t.Fatalf("artifact specs = %#v, want one forwardable content artifact", specs)
	}
	if specs[0].ProviderReturn == nil || specs[0].ProviderReturn.StepType != "step.s3_download" {
		t.Fatalf("provider return = %#v, want step.s3_download", specs[0].ProviderReturn)
	}
}

func TestS3ContentSourceRuntimeAdapterContract(t *testing.T) {
	contracts := RuntimeAdapterContracts()
	if len(contracts) != 1 {
		t.Fatalf("RuntimeAdapterContracts len = %d, want 1", len(contracts))
	}
	contract := contracts[0]
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !contract.Supports(core.WorkloadProvider) {
		t.Fatalf("runtime adapter workload kinds = %#v, want provider", contract.WorkloadKinds)
	}
	if !contract.SupportsAdapterKind(core.RuntimeAdapterExecution) {
		t.Fatalf("runtime adapter kinds = %#v, want execution", contract.Kinds)
	}
}

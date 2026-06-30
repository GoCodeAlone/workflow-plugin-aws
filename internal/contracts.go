package internal

import core "github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"

const (
	awsContentSourceContractVersion = "v0.1.0"

	awsS3ContentSourceProvider = "s3-content-source"
	awsS3ContentExecutor       = "aws-s3-fetch"
	awsS3ContentConformance    = "aws-s3-fetch-v1"
)

var (
	awsS3ConfigSchemaDigest = core.CanonicalHash(map[string]any{
		"type": "object",
		"required": []string{
			"bucket",
			"region",
		},
		"properties": map[string]any{
			"bucket":      map[string]any{"type": "string"},
			"region":      map[string]any{"type": "string"},
			"endpoint":    map[string]any{"type": "string"},
			"storage_ref": map[string]any{"type": "string"},
		},
	})
	awsS3OperationInputDigest = core.CanonicalHash(map[string]any{
		"type":     "object",
		"required": []string{"key", "artifact"},
		"properties": map[string]any{
			"bucket":       map[string]any{"type": "string"},
			"key":          map[string]any{"type": "string"},
			"storage_ref":  map[string]any{"type": "string"},
			"artifact":     map[string]any{"type": "string"},
			"content_type": map[string]any{"type": "string"},
		},
	})
	awsS3OperationOutputDigest = core.CanonicalHash(map[string]any{
		"type":     "object",
		"required": []string{"artifact", "content_ref", "bucket", "key"},
		"properties": map[string]any{
			"artifact":     map[string]any{"type": "string"},
			"content_ref":  map[string]any{"type": "string"},
			"bucket":       map[string]any{"type": "string"},
			"key":          map[string]any{"type": "string"},
			"content_type": map[string]any{"type": "string"},
			"sha256":       map[string]any{"type": "string"},
		},
	})
	awsS3RuntimeImageDigest  = core.CanonicalHash("workflow-plugin-aws:s3-content-source:image")
	awsS3RuntimeRootFSDigest = core.CanonicalHash("workflow-plugin-aws:s3-content-source:rootfs")
)

func ProviderContracts() []core.ProviderContract {
	return []core.ProviderContract{S3ContentSourceProviderContract()}
}

func S3ContentSourceProviderContract() core.ProviderContract {
	tier := core.ExecutionHardenedContainer
	proof := core.ProofArtifactHash
	runtimeProfile := core.DefaultProviderRuntimeProfile(awsS3ContentExecutor, tier, proof)
	runtimeProfile.ID = awsS3ContentExecutor + "-" + string(tier) + "-" + string(proof) + "-runtime"
	runtimeProfile.ConformanceProfiles = append(runtimeProfile.ConformanceProfiles, awsS3ContentConformance)

	return core.ProviderContract{
		ProtocolVersion:    core.Version,
		ID:                 "workflow-plugin-aws.s3-content-source.v1",
		PluginID:           "workflow-plugin-aws",
		ProviderID:         awsS3ContentSourceProvider,
		ContractID:         "workflow-plugin-aws.s3-content-source.v1",
		Version:            awsContentSourceContractVersion,
		DisplayName:        "AWS S3 content-source provider",
		ConfigSchemaRef:    "schema://providers/workflow-plugin-aws/s3-content-source/config/v1",
		ConfigSchemaDigest: awsS3ConfigSchemaDigest,
		OperatingModes: []core.NetworkOperatingMode{
			core.NetworkModeBatch,
		},
		WorkloadKinds: []string{string(core.WorkloadProvider)},
		ExecutorProviders: []string{
			awsS3ContentExecutor,
		},
		ExecutionSecurityTiers: []core.ExecutionSecurityTier{
			tier,
		},
		ProofTiers: []core.ProofTier{
			proof,
		},
		NetworkModes: []core.NetworkMode{
			core.NetworkModeOffline,
		},
		Operations: []core.ProviderOperation{{
			ID:                 "s3_fetch",
			InputSchemaRef:     "schema://providers/workflow-plugin-aws/s3-content-source/s3-fetch/input/v1",
			InputSchemaDigest:  awsS3OperationInputDigest,
			OutputSchemaRef:    "schema://providers/workflow-plugin-aws/s3-content-source/s3-fetch/output/v1",
			OutputSchemaDigest: awsS3OperationOutputDigest,
			Artifacts:          []string{"content"},
			ArtifactSpecs: []core.ProviderArtifactSpec{{
				Name:        "content",
				Required:    true,
				Forwardable: true,
				ProviderReturn: &core.ProviderArtifactReturnSpec{
					StepType:        "step.s3_download",
					Contract:        "workflow-plugin-aws:step.s3_download",
					ContractVersion: awsContentSourceContractVersion,
					RequiredConfig:  []string{"bucket", "region", "key"},
					OutputHandling:  []string{"artifact_ref", "content_ref", "sha256"},
				},
			}},
		}},
		RuntimeContract: core.ProviderRuntimeContract{
			Profiles: []core.ProviderRuntimeProfile{runtimeProfile},
		},
	}
}

func RuntimeAdapterContracts() []core.RuntimeAdapterContract {
	return []core.RuntimeAdapterContract{{
		ProtocolVersion: core.Version,
		AdapterID:       awsS3ContentExecutor,
		Descriptor: core.RuntimeDescriptor{
			Name:                  awsS3ContentExecutor,
			Version:               awsContentSourceContractVersion,
			ExecutionSecurityTier: core.ExecutionHardenedContainer,
			ProofTier:             core.ProofArtifactHash,
			ImageDigest:           awsS3RuntimeImageDigest,
			RootFSDigest:          awsS3RuntimeRootFSDigest,
		},
		Kinds: []core.RuntimeAdapterKind{
			core.RuntimeAdapterExecution,
		},
		WorkloadKinds: []core.WorkloadKind{
			core.WorkloadProvider,
		},
		RuntimeProfiles: []core.RuntimeProfile{
			core.RuntimeProfileSandboxedOCI,
		},
		WorkspacePolicy: core.RuntimeWorkspaceRequired,
		ConformanceProfiles: []string{
			awsS3ContentConformance,
		},
		Metadata: map[string]string{
			"storage_module": "storage.s3",
			"step_type":      "step.s3_download",
			"operation":      "s3_fetch",
		},
	}}
}

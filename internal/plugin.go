// Package internal implements the AWS workflow plugin.
package internal

import (
	"fmt"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/contracts"
	"github.com/GoCodeAlone/workflow-plugin-aws/provider"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
)

// Version is set at build time via -ldflags
// "-X github.com/GoCodeAlone/workflow-plugin-aws/internal.Version=X.Y.Z".
// Default is a bare semver so plugin loaders that validate semver accept
// unreleased dev builds; goreleaser overrides with the real release tag.
var Version = "0.0.0"

type awsPlugin struct{}

// NewAWSPlugin returns the AWS SDK plugin provider.
func NewAWSPlugin() sdk.PluginProvider {
	return &awsPlugin{}
}

func (p *awsPlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-aws",
		Version:     provider.ProviderVersion,
		Author:      "GoCodeAlone",
		Description: "AWS provider plugin for workflow IaC — manages ECS, EKS, RDS, ElastiCache, VPC, ALB, Route53, ECR, API Gateway, Security Groups, IAM, S3, and ACM resources",
	}
}

// ModuleTypes returns the module type names this plugin provides.
func (p *awsPlugin) ModuleTypes() []string {
	return []string{"iac.provider"}
}

// CreateModule creates a module instance of the given type using a legacy
// map-based config. Prefer CreateTypedModule for strict typed config.
func (p *awsPlugin) CreateModule(typeName, name string, config map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "iac.provider":
		return newIaCProviderModule(name, config), nil
	default:
		return nil, fmt.Errorf("unknown module type: %s", typeName)
	}
}

// TypedModuleTypes returns the module type names for which strict typed config
// is supported.
func (p *awsPlugin) TypedModuleTypes() []string {
	return []string{"iac.provider"}
}

// CreateTypedModule creates a typed module instance after unpacking and
// validating the AWSProviderConfig protobuf Any payload.
func (p *awsPlugin) CreateTypedModule(typeName, name string, config *anypb.Any) (sdk.ModuleInstance, error) {
	factory := sdk.NewTypedModuleFactory(
		"iac.provider",
		&contracts.AWSProviderConfig{},
		func(name string, cfg *contracts.AWSProviderConfig) (sdk.ModuleInstance, error) {
			legacyConfig := map[string]any{
				"region":            cfg.GetRegion(),
				"access_key_id":     cfg.GetAccessKeyId(),
				"secret_access_key": cfg.GetSecretAccessKey(),
				"ecs_cluster":       cfg.GetEcsCluster(),
			}
			return newIaCProviderModule(name, legacyConfig), nil
		},
	)
	return factory.CreateTypedModule(typeName, name, config)
}

// ContractRegistry returns strict protobuf contract descriptors for every
// module type this plugin advertises.
func (p *awsPlugin) ContractRegistry() *pb.ContractRegistry {
	return &pb.ContractRegistry{
		Contracts: []*pb.ContractDescriptor{
			{
				Kind:          pb.ContractKind_CONTRACT_KIND_MODULE,
				ModuleType:    "iac.provider",
				ConfigMessage: "workflow.plugins.aws.v1.AWSProviderConfig",
				Mode:          pb.ContractMode_CONTRACT_MODE_STRICT_PROTO,
			},
		},
		FileDescriptorSet: &descriptorpb.FileDescriptorSet{
			File: []*descriptorpb.FileDescriptorProto{
				protodesc.ToFileDescriptorProto(contracts.File_internal_contracts_aws_proto),
			},
		},
	}
}

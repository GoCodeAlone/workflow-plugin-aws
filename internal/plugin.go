// Package internal implements the AWS workflow plugin.
package internal

import (
	"fmt"

	"github.com/GoCodeAlone/workflow-plugin-aws/provider"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

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

func (p *awsPlugin) ModuleTypes() []string {
	return []string{"iac.provider"}
}

func (p *awsPlugin) CreateModule(typeName, name string, config map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "iac.provider":
		return newIaCProviderModule(name, config), nil
	default:
		return nil, fmt.Errorf("unknown module type: %s", typeName)
	}
}

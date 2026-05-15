// Package modules implements the aws plugin's standalone-module surface:
// plugin-native modules (aws.credentials, storage.s3, step.s3_upload, ...)
// the host constructs via the IaC plugin's CreateModule path (plan-2 PR 1
// wired sdk.IaCServeOptions.Modules into the iacGRPCPlugin bridge).
//
// aws.credentials is the optional DRY module: a config declares credentials
// once under a module name; sibling modules (storage.s3, step.s3_upload,
// etc.) reference them via `credentials_ref:` instead of repeating the
// inline `credentials:` block.
package modules

import (
	"context"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/credref"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// AWSCredentialsProvider implements sdk.ModuleProvider for the
// "aws.credentials" standalone-module type.
type AWSCredentialsProvider struct{}

// NewAWSCredentialsProvider returns a fresh provider.
func NewAWSCredentialsProvider() *AWSCredentialsProvider {
	return &AWSCredentialsProvider{}
}

// ModuleTypes reports the single module type this Provider serves.
func (p *AWSCredentialsProvider) ModuleTypes() []string {
	return []string{"aws.credentials"}
}

// CreateModule parses the YAML `credentials:` block (plus the top-level
// `region` field) into an awscreds.CredInput and registers it in the
// process-local credref registry under the module name. A duplicate name
// fails loudly — credentials_ref names must be unique.
//
// CredInput.Source is populated from `credentials.type` (the YAML field —
// "static" | "env" | "profile" | "role_arn") so BuildAWSConfig honours
// the user-declared source path. The field is NOT read from
// CloudAccount.Extra (which never crosses the gRPC boundary).
func (p *AWSCredentialsProvider) CreateModule(_, name string, config map[string]any) (sdk.ModuleInstance, error) {
	credsMap, _ := config["credentials"].(map[string]any)
	c := awscreds.CredInput{
		Region:       stringField(config, "region"),
		AccessKey:    stringField(credsMap, "accessKey"),
		SecretKey:    stringField(credsMap, "secretKey"),
		SessionToken: stringField(credsMap, "sessionToken"),
		RoleARN:      stringField(credsMap, "roleArn"),
		ExternalID:   stringField(credsMap, "externalId"),
		Profile:      stringField(credsMap, "profile"),
		SessionName:  stringField(credsMap, "sessionName"),
		Source:       stringField(credsMap, "type"),
	}
	if err := credref.Register(name, c); err != nil {
		return nil, err
	}
	return &awsCredentialsInstance{name: name}, nil
}

// awsCredentialsInstance is the lifecycle-only module instance the
// aws.credentials Provider returns. The actual credential effect was the
// credref.Register call in CreateModule; the instance itself has no
// runtime behavior.
type awsCredentialsInstance struct {
	name string
}

func (m *awsCredentialsInstance) Init() error                  { return nil }
func (m *awsCredentialsInstance) Start(_ context.Context) error { return nil }
func (m *awsCredentialsInstance) Stop(_ context.Context) error  { return nil }

func stringField(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

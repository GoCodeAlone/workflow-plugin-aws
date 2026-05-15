// plugin_options.go — single source of truth for the providers wired into
// sdk.IaCServeOptions. main.go and the host-conformance parity test both
// consume these helpers so plugin.json declarations and the running plugin's
// surface cannot drift.
package internal

import (
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/modules"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/steps"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// ModuleProviders returns the type-name → sdk.ModuleProvider map the plugin
// surfaces via IaCServeOptions.Modules.
//
// The map keys MUST equal plugin.json `capabilities.moduleTypes`; the parity
// test in host_conformance_test.go enforces that invariant.
func ModuleProviders() map[string]sdk.ModuleProvider {
	return map[string]sdk.ModuleProvider{
		"aws.credentials": modules.NewAWSCredentialsProvider(),
		"storage.s3":      modules.NewS3StorageProvider(),
	}
}

// StepProviders returns the type-name → sdk.StepProvider map the plugin
// surfaces via IaCServeOptions.Steps.
//
// The map keys MUST equal plugin.json `capabilities.stepTypes`; the parity
// test in host_conformance_test.go enforces that invariant.
func StepProviders() map[string]sdk.StepProvider {
	return map[string]sdk.StepProvider{
		"step.s3_upload": steps.NewS3UploadStepProvider(),
	}
}

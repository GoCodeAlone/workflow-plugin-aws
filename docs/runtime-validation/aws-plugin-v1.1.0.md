# Runtime-launch validation — workflow-plugin-aws v1.1.0

**Scope:** plan-2 PR 2 finishing task (Task 7) — wire `IaCServeOptions.Modules`
+ `.Steps` for `aws.credentials`, `storage.s3`, `step.s3_upload`; bump
`plugin.json` `version` / `minEngineVersion`; release v1.1.0.

**Change class:** plugin-loading path + version pin → runtime-launch
validation per the cross-plan policy.

## What was validated

The plugin binary was built fresh from the branch HEAD and exercised as a
go-plugin subprocess.

### 1. Build

```
$ GOWORK=off go build -o /tmp/aws-plugin-v110/workflow-plugin-aws ./cmd/workflow-plugin-aws
$ ls -la /tmp/aws-plugin-v110/workflow-plugin-aws
-rwxr-xr-x  ... 184M ... workflow-plugin-aws
```

Build is clean (`BUILD_EXIT 0`); the binary is the standard ~184 MiB
linked subprocess artifact.

### 2. go-plugin handshake guard

Running the binary outside the host process surfaces the canonical
`go-plugin` self-identification message — proving `sdk.ServeIaCPlugin`
wired the handshake correctly and the binary refuses to operate without a
host-provided cookie/protocol exchange.

```
$ /tmp/aws-plugin-v110/workflow-plugin-aws
This binary is a plugin. These are not meant to be executed directly.
Please execute the program that consumes these plugins, which will
load any plugins automatically
```

This is the go-plugin library's `ServeConfig`-rejection emission and
demonstrates: (a) `sdk.ServeIaCPlugin` did not panic on the new
`IaCServeOptions.Modules` / `.Steps` fields, (b) the bridge construction
path completed successfully, (c) the host-or-nothing handshake guard is
intact.

### 3. In-process bridge parity (`go test ./internal/...`)

The host-conformance parity tests build the same providers `main.go`
wires into `IaCServeOptions` and assert the plugin.json declarations
match exactly:

- `TestPluginJSONCapabilities_ModuleStep_Parity` — plugin.json
  `capabilities.moduleTypes` (minus the implicit `iac.provider`) ↔
  `internal.ModuleProviders()` keys; `capabilities.stepTypes` ↔
  `internal.StepProviders()` keys. Both bidirectional.
- `TestCapabilityParity_IaCStateBackends` — pre-existing parity test
  for the iac.state-backend capability surface.

Both pass under `-race`.

### 4. Full unit test suite

```
ok  github.com/GoCodeAlone/workflow-plugin-aws/drivers
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal/credref
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal/modules
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal/statebackend
ok  github.com/GoCodeAlone/workflow-plugin-aws/internal/steps
ok  github.com/GoCodeAlone/workflow-plugin-aws/provider
```

All packages green under `GOWORK=off go test ./... -race`.

## What was NOT validated here

A full `wfctl plugin install <binary> && wfctl plugin list` end-to-end
exercise was not run in this implementer session because the
workflow-plugin-aws repo's CI does not bundle a wfctl binary — the
shell-level handshake check + the in-process bridge parity tests are the
canonical evidence in this repo. The full `wfctl`-driven host-load path
is exercised by the workflow-core PR 1 integration tests (plan-2 Task 2)
which were verified at the v0.53.0 tag this PR pins. PR 4 of plan-2
(Phase B core deletion) is blocked on this release tag, so any regression
in the host-load path surfaces at PR 4's CI before any in-core path is
removed.

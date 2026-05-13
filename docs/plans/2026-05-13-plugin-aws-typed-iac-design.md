# Design: AWS Plugin Typed-IaC Conformance Migration (Issue #8)

**Date:** 2026-05-13
**Author:** Claude Code (autonomous pipeline)
**Status:** Draft → Approved

## Context

workflow-plugin-aws v0.2.0 still uses the legacy `sdk.Serve(internal.NewAWSPlugin())`
string-dispatch surface (`plugin.go` / `module.go` / `internal/typed.go`). The workflow
engine's strict-contracts force-cutover (v0.50.0+) removed this path from the host side.
Issue #8 asks that the AWS plugin adopt the same typed-IaC gRPC pattern that
workflow-plugin-digitalocean v1.0.1 shipped under.

## Precedent

workflow-plugin-digitalocean v1.0.1 (the "force-cutover" reference):
- `cmd/plugin/main.go` calls `sdk.ServeIaCPlugin(internal.NewIaCServer(), sdk.IaCServeOptions{})`
- `internal/iacserver.go` — `doIaCServer` embeds all `pb.Unimplemented*Server` types,
  implements every required RPC, and delegates to the underlying `*DOProvider`
- `internal/resourcedriver_server.go` — `pb.ResourceDriverServer` per-type CRUD dispatch
- `internal/provider.go` — `DOProvider` unchanged; server wraps it
- Pinned to `workflow v0.51.2`

## Approach (single chosen option)

**Single-PR force-cutover mirroring DO v1.0.1.**

No compat shim. The legacy `internal/plugin.go`, `internal/module.go`, and
`internal/typed.go` (from the `strict-contracts` branch) are deleted. The new entrypoint
calls `sdk.ServeIaCPlugin`. The plugin surfaces every typed gRPC service that
`*AWSProvider` satisfies at the Go interface level.

Alternatives considered:
- **Keep `sdk.Serve` + add `InvokeTypedMethod` bridge** — rejected: this is the old
  `strict-contracts` branch approach, incompatible with engine v0.50.0+.
- **Two-PR (add typed server first, remove legacy second)** — rejected per memory
  `feedback_force_strict_contracts_no_compat`: one combined cutover PR is correct.

## Scope

### Phase 1 — Typed server layer (new files)

| File | Action |
|------|--------|
| `internal/iacserver.go` | NEW: `awsIaCServer` struct with required + optional pb service methods |
| `internal/resourcedriver_server.go` | NEW: ResourceDriver CRUD dispatch |

`awsIaCServer` embeds:
```
pb.UnimplementedIaCProviderRequiredServer
pb.UnimplementedIaCProviderEnumeratorServer        // optional, not yet implemented
pb.UnimplementedIaCProviderDriftDetectorServer
pb.UnimplementedIaCProviderCredentialRevokerServer // optional, not yet implemented
pb.UnimplementedIaCProviderMigrationRepairerServer // optional, not yet implemented
pb.UnimplementedIaCProviderValidatorServer         // optional, not yet implemented
pb.UnimplementedIaCProviderDriftConfigDetectorServer
pb.UnimplementedResourceDriverServer
```

**What gets implemented** (methods `*AWSProvider` actually supports):
- All `IaCProviderRequiredServer` methods: `Initialize`, `Name`, `Version`,
  `Capabilities`, `Plan`, `Apply`, `Destroy`, `Status`, `Import`, `ResolveSizing`,
  `BootstrapStateBackend`
- `IaCProviderDriftDetectorServer.DetectDrift` (AWSProvider has this)
- `ResourceDriverServer` 9 CRUD methods (AWSProvider.ResourceDriver works)

**What is left as Unimplemented** (AWSProvider does NOT have these yet):
- `EnumerateAll`, `EnumerateByTag` — no AWS tag-query implementation
- `RevokeProviderCredential` — no AWS credential rotation
- `RepairDirtyMigration` — no migration repair
- `ValidatePlan` — no cross-resource plan validator
- `DetectDriftConfig` / `DetectDriftWithSpecs` — no spec-aware drift

Marshalling helpers (pb↔Go): copy pattern exactly from DO `iacserver.go`.
No JSON↔structpb conversion — config/outputs cross as `config_json`/`outputs_json`
(JSON bytes), matching the hard invariant.

### Phase 2 — Entrypoint cutover

| File | Action |
|------|--------|
| `cmd/workflow-plugin-aws/main.go` | Change `sdk.Serve(NewAWSPlugin())` → `sdk.ServeIaCPlugin(internal.NewIaCServer(), sdk.IaCServeOptions{})` |
| `internal/plugin.go` | DELETE |
| `internal/module.go` | DELETE |

### Phase 3 — Version/metadata updates

| File | Action |
|------|--------|
| `go.mod` | Bump `workflow v0.19.2` → `v0.51.7` |
| `plugin.json` | Bump `version` to `1.0.0`, `minEngineVersion` to `0.51.0` |
| `provider/provider.go` | Bump `ProviderVersion` constant to `1.0.0` |

### Phase 4 — Tests

| File | Action |
|------|--------|
| `internal/iacserver_test.go` | NEW: unit tests for all server methods (mock provider) |
| `internal/host_conformance_test.go` | Update: change expected plugin load path from legacy to ServeIaCPlugin |
| `internal/plugin_test.go` | DELETE (tests the legacy PluginProvider path) |

### Phase 5 — CI

CI already runs `go test ./...` and the host-conformance gate. No new gates needed.
The `WORKFLOW_IAC_HOST_CONFORMANCE=1` gate in `host_conformance_test.go` will be
updated to validate the typed-IaC load path.

## Compile-time guards

```go
var (
    _ pb.IaCProviderRequiredServer            = (*awsIaCServer)(nil)
    _ pb.IaCProviderDriftDetectorServer       = (*awsIaCServer)(nil)
    _ pb.ResourceDriverServer                 = (*awsIaCServer)(nil)
)
```

Optional services auto-registered by `sdk.RegisterAllIaCProviderServices` when satisfied
at the Go type level — no manual registration required.

## Wire invariants (from strict-contracts hard invariants)

- NO `structpb.Struct` on the wire
- NO `Any.UnmarshalTo` for config/outputs — use `config_json` / `outputs_json` (JSON bytes)
- Outputs that are `map[string]any` are marshalled to JSON, never via `structpb.NewStruct`
- Typed slices (`[]string`, `[]X`) are safe because `pb.ResourceOutput.outputs_json` is `bytes`
  — no structpb round-trip

## Rollback

This is a gRPC protocol change at the plugin boundary. Rollback = revert the commit
and retag v0.2.0. Consumers must pin the old tag explicitly. No database migrations,
no state mutations, no side effects outside the plugin binary.

Old workflow engine tags (pre-v0.50.0) are permanently incompatible after this PR merges —
per the force-cutover mandate (`feedback_force_strict_contracts_no_compat`).

## Assumptions

1. `sdk.ServeIaCPlugin` and `sdk.RegisterAllIaCProviderServices` are present and stable
   in workflow v0.51.7 (confirmed: DO v1.0.1 used v0.51.2 with this API).
2. `pb.Unimplemented*Server` embeds satisfy the forward-compat contract for optional
   services not yet implemented by `*AWSProvider`.
3. The `host_conformance_test.go` can be updated to validate the typed-IaC load path
   without requiring a live AWS credential.
4. `workflow v0.51.7` is the latest stable tag (confirmed via `git tag`).
5. `*AWSProvider.DetectDrift` only implements existence-check (no spec comparison),
   so `DriftConfigDetector` remains Unimplemented — conservative but correct.
6. The `strict-contracts` local branch's `internal/typed.go` uses the old
   `InvokeTypedMethod` string-dispatch (predates force-cutover) and must NOT be merged.

## Open questions resolved

- Q: Should we implement `ValidatePlan`? A: No — AWSProvider has no cross-resource
  constraint validator yet. Unimplemented embed is forward-compatible.
- Q: Multi-PR vs single-PR? A: Single-PR force-cutover per precedent and memory
  `feedback_force_strict_contracts_no_compat`.
- Q: Keep `internal/contracts/aws.proto` / `AWSProviderConfig`? A: Yes — the proto
  config message is still valid for `plugin.contracts.json` strict module config.
  The typed module factory in `CreateTypedModule` becomes irrelevant after deletion
  of `plugin.go`, but the proto descriptor remains useful for documentation.

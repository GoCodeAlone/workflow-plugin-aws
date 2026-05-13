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

`awsIaCServer` embeds (forward-compat: every embed is present so future proto additions
do not break the build; only services with actual method implementations are
meaningfully registered by `sdk.RegisterAllIaCProviderServices`):
```
pb.UnimplementedIaCProviderRequiredServer
pb.UnimplementedIaCProviderEnumeratorServer        // forward-compat only; no AWS impl yet
pb.UnimplementedIaCProviderDriftDetectorServer
pb.UnimplementedIaCProviderCredentialRevokerServer // forward-compat only; no AWS impl yet
pb.UnimplementedIaCProviderMigrationRepairerServer // forward-compat only; no AWS impl yet
pb.UnimplementedIaCProviderValidatorServer         // forward-compat only; no AWS impl yet
pb.UnimplementedIaCProviderDriftConfigDetectorServer
pb.UnimplementedResourceDriverServer
```

**What gets implemented** (methods `*AWSProvider` actually supports):
- All `IaCProviderRequiredServer` methods: `Initialize`, `Name`, `Version`,
  `Capabilities`, `Plan`, `Apply`, `Destroy`, `Status`, `Import`, `ResolveSizing`,
  `BootstrapStateBackend`
- `IaCProviderDriftDetectorServer.DetectDrift` AND `DetectDriftWithSpecs` —
  `DetectDrift` is the real impl (existence-check); `DetectDriftWithSpecs` is a
  thin delegator to `DetectDrift` (ignores the specs map, consistent with existence-only
  behavior). Both methods required for `IaCProviderDriftDetectorServer` to register cleanly.
- `ResourceDriverServer` 9 CRUD methods (AWSProvider.ResourceDriver works)

**What is left as Unimplemented** (forward-compat embed only, not auto-registered):
- `EnumerateAll`, `EnumerateByTag` — no AWS tag-query implementation
- `RevokeProviderCredential` — no AWS credential rotation
- `RepairDirtyMigration` — no migration repair
- `ValidatePlan` — no cross-resource plan validator
- `DetectDriftConfig` — DriftConfigDetector is a separate service; leave Unimplemented

Marshalling helpers (pb↔Go): copy pattern exactly from DO `iacserver.go`.
No JSON↔structpb conversion — config/outputs cross as `config_json`/`outputs_json`
(JSON bytes), matching the hard invariant.

### Phase 2 — Entrypoint cutover

**Note:** Phase 2 deletions and Phase 4 test deletions MUST happen atomically in
the same commit to avoid transient compile failures (`plugin_test.go` references
`iacProviderModule` from `module.go`; deleting `module.go` without deleting
`plugin_test.go` in the same commit breaks the build).

| File | Action |
|------|--------|
| `cmd/workflow-plugin-aws/main.go` | Change `sdk.Serve(NewAWSPlugin())` → `sdk.ServeIaCPlugin(internal.NewIaCServer(), sdk.IaCServeOptions{})` |
| `internal/plugin.go` | DELETE (atomically with plugin_test.go; see Phase 4) |
| `internal/module.go` | DELETE (atomically with plugin_test.go; see Phase 4) |

### Phase 3 — Version/metadata updates

**Note:** After bumping `go.mod`, run `go mod tidy && go build ./...` before
proceeding to write `iacserver.go`. The v0.19.2 → v0.51.7 jump (32 minor versions)
may introduce transitive dependency changes. Surface any API breaks in
`interfaces.*` or `plugin/external/proto/*` as a blocker before writing new code.

| File | Action |
|------|--------|
| `go.mod` | Bump `workflow v0.19.2` → `v0.51.7`; run `go mod tidy` |
| `plugin.json` | Bump `version` to `1.0.0`, `minEngineVersion` to `0.51.0` |
| `provider/provider.go` | Bump `ProviderVersion` constant to `1.0.0` |

### Phase 4 — Tests

**Deletion ordering:** `internal/plugin_test.go` and `internal/plugin.go` +
`internal/module.go` MUST be deleted in the same commit (see Phase 2 note).

| File | Action |
|------|--------|
| `internal/iacserver_test.go` | NEW: unit tests for all server methods (mock provider) |
| `internal/host_conformance_test.go` | Rewrite: match DO v1.0.1 pattern (see below) |
| `internal/plugin_test.go` | DELETE atomically with plugin.go + module.go |

**`host_conformance_test.go` rewrite spec** (must match DO v1.0.1 exactly):
1. Build plugin binary via `go build -o <tmpdir>/workflow-plugin-aws ./cmd/workflow-plugin-aws`
2. Load via `external.NewExternalPluginManager` + `LoadPlugin`
3. Assert `adapter.ContractRegistry()` contains a **service-kind** contract
   with `pb.IaCProviderRequired_ServiceDesc.ServiceName` (not module-kind)
4. Make a live `pb.NewIaCProviderRequiredClient(adapter.Conn()).Name()` RPC call
5. Make a live `required.Capabilities()` RPC call; assert `infra.container_service` present
6. These assertions mirror the DO `TestWorkflowHostConformance_LoadsTypedIaCPlugin` exactly

### Phase 5 — CI

CI already runs `go test ./...` and the host-conformance gate. No new gates needed.
The `WORKFLOW_IAC_HOST_CONFORMANCE=1` gate in `host_conformance_test.go` will be
updated to validate the typed-IaC load path (service-kind contract + live RPC).

## Compile-time guards

```go
var (
    _ pb.IaCProviderRequiredServer      = (*awsIaCServer)(nil)
    // IaCProviderDriftDetectorServer requires BOTH DetectDrift AND DetectDriftWithSpecs.
    // Both are implemented: DetectDrift is the real check; DetectDriftWithSpecs delegates
    // to DetectDrift and ignores the specs map (existence-only behavior).
    _ pb.IaCProviderDriftDetectorServer = (*awsIaCServer)(nil)
    _ pb.ResourceDriverServer           = (*awsIaCServer)(nil)
)
```

Optional services auto-registered by `sdk.RegisterAllIaCProviderServices` when satisfied
at the Go type level — no manual registration required.

**Service auto-registration behavior:**
- `IaCProviderDriftDetector` service IS registered (both methods implemented)
- `IaCProviderEnumerator` service is NOT registered (only embed, no real methods)
- `IaCProviderCredentialRevoker` service is NOT registered (only embed)
- `IaCProviderMigrationRepairer` service is NOT registered (only embed)
- `IaCProviderValidator` service is NOT registered (only embed)
- `IaCProviderDriftConfigDetector` service is NOT registered (only embed)

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

After reverting, run `go mod tidy` to restore go.sum to the v0.19.2-pinned state
(the workflow version bump changes transitive dependencies in go.sum; revert does
not auto-restore it).

Old workflow engine tags (pre-v0.50.0) are permanently incompatible after this PR merges —
per the force-cutover mandate (`feedback_force_strict_contracts_no_compat`).

## Assumptions

1. `sdk.ServeIaCPlugin` and `sdk.RegisterAllIaCProviderServices` are present and stable
   in workflow v0.51.7 (confirmed: DO v1.0.1 used v0.51.2 with this API).
2. `pb.Unimplemented*Server` embeds satisfy the forward-compat contract for optional
   services not yet implemented by `*AWSProvider`. Services where ONLY the embed is
   present are NOT auto-registered by the SDK (type-assertion fails) — so callers
   get "service not registered" rather than `codes.Unimplemented`.
3. The `host_conformance_test.go` can be updated to validate the typed-IaC load path
   (service-kind contract + live `Name()` + `Capabilities()` RPC) without requiring
   a live AWS credential — the plugin binary starts and responds to these RPCs without
   an initialized AWS session (same as DO pattern).
4. `workflow v0.51.7` is the latest stable tag (confirmed via `git tag`).
5. `*AWSProvider.DetectDrift` only implements existence-check (no spec comparison).
   `DetectDriftWithSpecs` is implemented as a thin delegator to `DetectDrift`.
   `DriftConfigDetector` (`DetectDriftConfig`) remains Unimplemented (different service).
6. The `strict-contracts` local branch's `internal/typed.go` uses the old
   `InvokeTypedMethod` string-dispatch (predates force-cutover) and must NOT be merged.
7. `plugin.contracts.json` is loaded by the engine manager from disk independent of
   the gRPC service registration (confirmed: DO v1.0.1 retains `plugin.contracts.json`
   and the engine loads it separately). The file remains valid after cutover.
8. The v0.19.2 → v0.51.7 go.mod bump will not break `interfaces.*` or
   `plugin/external/proto/*` APIs used by `AWSProvider` — confirmed by verifying
   DO v1.0.1 (which has the same interface surface) compiled cleanly at v0.51.2.

## Open questions resolved

- Q: Should we implement `ValidatePlan`? A: No — AWSProvider has no cross-resource
  constraint validator yet. Unimplemented embed is forward-compatible.
- Q: Multi-PR vs single-PR? A: Single-PR force-cutover per precedent and memory
  `feedback_force_strict_contracts_no_compat`.
- Q: Keep `internal/contracts/aws.proto` / `AWSProviderConfig`? A: Yes — the proto
  config message is still valid for `plugin.contracts.json` strict module config.
  The typed module factory in `CreateTypedModule` becomes irrelevant after deletion
  of `plugin.go`, but the proto descriptor remains useful for documentation.

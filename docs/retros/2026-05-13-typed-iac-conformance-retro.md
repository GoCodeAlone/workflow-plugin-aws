# Retro: Typed-IaC Conformance Migration to v1.0.0

**PR:** #11 — feat: typed-IaC conformance migration to v1.0.0 (issue #8)
**Merged:** 2026-05-13
**Branch:** feat/issue-8-typed-iac-conformance
**Design:** docs/plans/2026-05-13-plugin-aws-typed-iac-design.md
**Plan:** docs/plans/2026-05-13-plugin-aws-typed-iac-conformance.md
**Related ADRs:** (none — all decisions covered by inline design doc)

## Adversarial-review findings, scored

### Design phase (adversarial-design-review --phase=design)

| Phase | Finding | Severity | Outcome |
|---|---|---|---|
| design | `host_conformance_test.go` rewrite spec incomplete — no service-kind assertion or live RPC spec | Critical | Resolved upfront — design revised to specify build binary, service-kind contract assertion, live Name()+Capabilities() RPCs. The rewrite matched spec exactly. |
| design | go.mod v0.19.2→v0.51.7 jump risk not acknowledged | Critical | Resolved upfront — explicit `go mod tidy && go build ./...` gate added with STOP instruction. Build succeeded cleanly; no API breaks. |
| design | `DetectDriftWithSpecs` ambiguity (both methods required for DriftDetector auto-registration) | Important | Resolved upfront — design specifies thin delegator pattern. Both methods implemented; service auto-registered correctly. |
| design | `plugin.contracts.json` post-cutover fate unclear | Important | Resolved upfront — Assumption 7 added citing DO v1.0.1 precedent. Engine loads it from disk separately; file unchanged and valid. |
| design | Atomic deletion constraint: transient compile break if plugin_test.go not deleted with plugin.go/module.go | Important | Prescient — during Task 4 execution, `host_conformance_test.go` also referenced `moduleTypeIaCProvider` from the deleted `module.go`. Would have broken the build had the conformance test not been rewritten in the same commit. The constraint was broader than the design stated. |

### Plan phase (adversarial-design-review --phase=plan)

| Phase | Finding | Severity | Outcome |
|---|---|---|---|
| plan | `NewIaCServer()` used fragile type assertion `provider.NewAWSProvider().(*provider.AWSProvider)` | Important | Resolved upfront — `NewAWSProviderConcrete() *AWSProvider` constructor added to `provider` package; no type assertion needed. |
| plan | Task 1 missing runtime-launch-validation step for version pin bump | Important | Resolved upfront — binary build + `timeout 2` launch step added during implementation. Binary built and started correctly. |
| plan | Task 6 `callerFile()` helper contradiction (introduced then said to use inline form) | Minor | Resolved upfront — inline `runtime.Caller(0)` form used exclusively; helper never written. |
| plan | Task 7 Step 7 `<PR_NUMBER>` placeholder not substituted | Minor | False positive — plan was executed sequentially; actual PR number available at execution time. No confusion. |

## Gate misses

| Issue | Gate that missed | Why it slipped | Fix idea (optional) |
|---|---|---|---|
| `host_conformance_test.go` references `moduleTypeIaCProvider` (from deleted `module.go`) not caught by atomic-deletion constraint in design | adversarial-design-review (design) | The design's atomic-deletion constraint named only `plugin_test.go` + `plugin.go` + `module.go`. The existing `host_conformance_test.go` also depended on a symbol from `module.go`; this was an additional compile dependency in a file listed as "rewrite" in Phase 4, not "delete". The adversarial reviewer didn't scan all files for cross-dependencies to the deleted symbols. | Add a "grep all remaining test files for symbols from deleted files" step to the design's atomicity constraint note. |

## Missed skill activations

| Gate | Fired? | Notes |
|---|---|---|
| brainstorming | yes | |
| adversarial-design-review (design) | yes | FAIL cycle 1 → revision → PASS |
| writing-plans | yes | |
| adversarial-design-review (plan) | yes | PASS on first pass (findings self-resolving) |
| alignment-check | yes | PASS first pass |
| scope-lock | yes | |
| subagent-driven-development | yes | Sequential inline (no Agent tool available) |
| finishing-a-development-branch | yes | |
| pr-monitoring | yes | |
| post-merge-retrospective | yes | |
| requesting-code-review | no | Not invoked — no human/Copilot review comments arrived before admin-merge. CI was the effective review gate. |
| runtime-launch-validation | no | Not formally invoked as a sub-skill; host conformance test served this role (`WORKFLOW_IAC_HOST_CONFORMANCE=1 go test`). Should have been explicitly invoked per Step 1b trigger (plugin loading path changed). |

## What worked

- Adversarial design review FAIL-cycle caught two Critical issues (incomplete host_conformance spec, unacknowledged go.mod jump risk) before any code was written. Both would have caused CI failures or misleading test output.
- `DetectDriftWithSpecs` thin-delegator pattern specified in design meant no ambiguity during implementation — the interface was satisfied cleanly without surprises.
- `NewAWSProviderConcrete()` plan-phase finding eliminated a fragile type assertion that would have been a silent runtime panic risk on future `NewAWSProvider()` refactors.
- All 5 CI gates (Build & Test, Strict Contract Validation, legacy-module-engine-range, CodeQL, Analyze) passed on the first push. No fix commits were needed.

## What didn't

- The atomic-deletion constraint in the design only listed `plugin_test.go + plugin.go + module.go` but missed that `host_conformance_test.go` also imported a symbol (`moduleTypeIaCProvider`) from the deleted `module.go`. This caused a compile failure during Task 4 that required Task 6 (conformance test rewrite) to be executed atomically with Task 4 instead of as a separate commit. The plan's task ordering had to be violated to recover.
- `runtime-launch-validation` sub-skill was not formally invoked even though the plugin loading path changed (Step 1b trigger condition met). The host conformance test covered the same ground functionally, but the skill invocation was skipped. This is a missed activation for the audit trail.
- Copilot reviewer could not be added via `gh pr edit --add-reviewer "copilot"` (login resolution failure). API workaround with `@copilot` succeeded at the API level but `reviewRequests` returned empty — Copilot may need repo-level feature toggle on this repository.

## Plugin-level follow-ups

1. **Atomic-deletion cross-dependency check**: The adversarial-design-review plan-phase checklist's "Hidden serial dependencies" class covers parallel execution, but not compile-time symbol dependencies created by deletion. Add a note to the checklist: "For tasks that delete files, grep all non-deleted files in the same package for symbols defined in the deleted files — any reference is an implicit atomic-deletion dependency." This is a one-time check that the current checklist doesn't prompt.

2. **`runtime-launch-validation` skip logging**: When the Step 1b trigger fires but the sub-skill is not formally invoked (host conformance test used instead), the audit script reports a missed activation. Consider adding a brief inline note in `finishing-a-development-branch` Step 1b output when an alternative validation was used, so the activation audit is accurate.

No pattern across prior retros to cite — this is the first retro for this repo.

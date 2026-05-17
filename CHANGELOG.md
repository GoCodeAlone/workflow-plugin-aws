# Changelog

All notable changes to this project will be documented in this file.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.0.0-rc1] — 2026-05-17

### Breaking changes (workflow#699)

- Removed `AWSProvider.Apply` Go method (dead since v1.2.0 v2 dispatch declaration; never reached by wfctl after v2 routes through `wfctlhelpers.ApplyPlanWithHooks`).
- Removed `awsIaCServer.Apply` gRPC handler + `applyResultToPB` encoder helper. The proto-side `rpc Apply` was deleted in workflow v0.56.0-rc1.
- Requires workflow v0.56.0+ (was v0.54.0).

### Reason

Per ADR 0024 compile-time-safety mandate: hard-delete the dead v1 Apply surface across the IaC plugin ecosystem. Plugin's typed `CapabilitiesResponse.compute_plan_version = "v2"` declaration unchanged.

## [1.1.0] — 2026-05-15

### Added

- **`aws.credentials` standalone module** (`internal/modules/aws_credentials.go`):
  optional DRY module that lets a config declare AWS credentials once and have
  many sibling `storage.s3` / `step.s3_upload` modules reference them via
  `credentials_ref:`. Backed by a process-local `credref` registry that rejects
  duplicate names within a config.
- **`storage.s3` standalone module** (`internal/modules/storage_s3.go`): the
  S3-backed storage module, plugin-native via `IaCServeOptions.Modules`.
  Credentials inline (`credentials:` sub-block) or `credentials_ref:` a
  sibling `aws.credentials` module. Optional `endpoint` override (MinIO /
  LocalStack via path-style addressing).
- **`step.s3_upload` standalone step** (`internal/steps/s3_upload.go`):
  pipeline step that uploads a base64-encoded body from a dot-path in the
  pipeline context to S3 and returns `{url, key, bucket}` as step output.
  Supports `{{ .field }}` / `{{ uuid }}` key templates and
  `content_type_from` dot-path resolution. Plugin-native via
  `IaCServeOptions.Steps`.
- **`internal/awscreds.BuildAWSConfig`** — in-plugin AWS credential
  resolution. Handles all 4 source paths from the YAML `credentials.type`
  field: `static` (inline keys), `env` / `""` (aws-sdk-go-v2 default chain),
  `profile` (shared-config profile via `config.WithSharedConfigProfile`),
  `role_arn` (STS `AssumeRole` on top of base creds). Ports the SDK-bearing
  resolver bodies from workflow core's `module/cloud_account_aws_creds.go`,
  which the matched workflow-core change rewrites to declare-only markers.
- **IaC provider credential path** now routes through `BuildAWSConfig` so a
  `credentials:` sub-block on the IaC provider config honours all 4 source
  paths (previously only inline static keys at top-level were recognised).
- **`TestPluginJSONCapabilities_ModuleStep_Parity`** — host-conformance test
  that asserts plugin.json `capabilities.moduleTypes` /
  `capabilities.stepTypes` exactly match the providers wired into
  `IaCServeOptions`.

### Changed

- **`plugin.json` `version`**: 1.0.0 → 1.1.0 (compatibility-marker minor bump
  for the new module / step capabilities).
- **`plugin.json` `minEngineVersion`**: 0.52.0 → 0.53.0 — requires workflow
  v0.53.0+ for the `IaCServeOptions.Modules` / `.Steps` bridge wiring (the
  plan-2 PR 1 SDK extension).
- **`plugin.json` `capabilities.moduleTypes`**: adds `aws.credentials` and
  `storage.s3` alongside the existing `iac.provider`.
- **`plugin.json` `capabilities.stepTypes`**: adds `step.s3_upload`.
- **`go.mod`** pins `github.com/GoCodeAlone/workflow v0.53.0`.

### Notes

- Phase-B core PR (workflow plan-2 Task 14/15) deletes in-core
  `iac_state_spaces.go` and `s3_storage.go` / `pipeline_step_s3_upload.go`;
  it is blocked on this release tag.
- Runtime-launch validation transcript:
  `docs/runtime-validation/aws-plugin-v1.1.0.md`.

## [1.0.0] — earlier

- Typed-IaC migration; baseline AWS provider surface (ECS / EKS / RDS /
  ElastiCache / VPC / ALB / Route53 / ECR / API Gateway / SecurityGroup /
  IAM / S3 / ACM / AutoScaling) + `iac.state s3` backend.

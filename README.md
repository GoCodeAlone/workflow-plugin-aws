# workflow-plugin-aws

> ⚠️ **Experimental** — This plugin compiles and passes its unit tests but has not been validated in any active GoCodeAlone-internal production deployment. Use with caution. Please [open an issue](https://github.com/GoCodeAlone/workflow-plugin-aws/issues/new) if you adopt it so we can promote it to **verified** status.

[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/GoCodeAlone/workflow-plugin-aws.svg)](https://pkg.go.dev/github.com/GoCodeAlone/workflow-plugin-aws)

AWS provider plugin for workflow IaC — manages ECS, EKS, RDS, ElastiCache, VPC, ALB, Route53, ECR, API Gateway, Security Groups, IAM, S3, ACM, and AutoScaling Group resources.

## What it provides

**Module types:**
- `iac.provider` — AWS IaC provider (v2 compute-plan dispatch)
- `aws.credentials` — AWS credential configuration module
- `storage.s3` — S3 storage backend module

**Pipeline step types:**
- `step.s3_upload` — Upload files to S3 from a pipeline step

**IaC state backends:**
- `s3` — Remote state stored in S3

## Install

```yaml
# In your wfctl.yaml
version: 1
plugins:
  - name: workflow-plugin-aws
    version: v1.2.1
    source: github.com/GoCodeAlone/workflow-plugin-aws
```

Then:

```sh
wfctl plugin install
```

## Minimal example

See [`examples/minimal/config.yaml`](examples/minimal/config.yaml).

**Required environment variables:**

| Variable | Description |
|----------|-------------|
| `AWS_REGION` | AWS region (e.g. `us-east-1`) |
| `AWS_ACCESS_KEY_ID` | AWS access key ID |
| `AWS_SECRET_ACCESS_KEY` | AWS secret access key |

Alternatively, configure an IAM role on the host (the plugin respects the standard AWS credential chain).

## Documentation

- [Plugin authoring guide (upstream)](https://github.com/GoCodeAlone/workflow/blob/main/docs/PLUGIN_AUTHORING.md)
- [Workflow engine docs](https://github.com/GoCodeAlone/workflow)
- [IaC guide](https://github.com/GoCodeAlone/workflow/blob/main/docs/iac/)

## License

MIT. See [LICENSE](LICENSE).

package provider_test

import (
	"testing"

	"github.com/GoCodeAlone/workflow/wftest"
)

// TestIntegration_AWSProvisionPipeline tests a typical AWS infrastructure
// provisioning pipeline with a mocked ECS cluster provisioning step.
func TestIntegration_AWSProvisionPipeline(t *testing.T) {
	provisionRec := wftest.RecordStep("step.aws_provision")
	provisionRec.WithOutput(map[string]any{
		"resource_id": "arn:aws:ecs:us-east-1:123456789:cluster/my-cluster",
		"status":      "created",
	})

	h := wftest.New(t,
		wftest.WithYAML(`
pipelines:
  provision-aws:
    steps:
      - name: prepare
        type: step.set
        config:
          values:
            region: us-east-1
            env: production
      - name: provision
        type: step.aws_provision
        config:
          resource_type: infra.container_service
          region: us-east-1
`),
		provisionRec,
	)

	result := h.ExecutePipeline("provision-aws", nil)
	if result.Error != nil {
		t.Fatalf("pipeline failed: %v", result.Error)
	}
	if !result.StepExecuted("prepare") {
		t.Error("prepare step should have executed")
	}
	if !result.StepExecuted("provision") {
		t.Error("provision step should have executed")
	}
	if provisionRec.CallCount() != 1 {
		t.Errorf("expected 1 call to step.aws_provision, got %d", provisionRec.CallCount())
	}
	calls := provisionRec.Calls()
	if calls[0].Config["resource_type"] != "infra.container_service" {
		t.Errorf("expected resource_type=infra.container_service, got %v", calls[0].Config["resource_type"])
	}

	out := result.StepOutput("provision")
	if out["status"] != "created" {
		t.Errorf("expected status=created, got %v", out["status"])
	}
	if out["resource_id"] != "arn:aws:ecs:us-east-1:123456789:cluster/my-cluster" {
		t.Errorf("unexpected resource_id: %v", out["resource_id"])
	}
}

// TestIntegration_AWSDestroyPipeline tests a destroy pipeline that tears down
// AWS resources using a mocked destroy step.
func TestIntegration_AWSDestroyPipeline(t *testing.T) {
	destroyRec := wftest.RecordStep("step.aws_destroy")
	destroyRec.WithOutput(map[string]any{
		"status":      "deleted",
		"resource_id": "arn:aws:ecs:us-east-1:123456789:cluster/my-cluster",
	})

	h := wftest.New(t,
		wftest.WithYAML(`
pipelines:
  destroy-aws:
    steps:
      - name: set-target
        type: step.set
        config:
          values:
            resource_id: arn:aws:ecs:us-east-1:123456789:cluster/my-cluster
            region: us-east-1
      - name: destroy
        type: step.aws_destroy
        config:
          resource_type: infra.container_service
          region: us-east-1
      - name: confirm
        type: step.set
        config:
          values:
            teardown_complete: "true"
`),
		destroyRec,
	)

	result := h.ExecutePipeline("destroy-aws", map[string]any{
		"resource_id": "arn:aws:ecs:us-east-1:123456789:cluster/my-cluster",
	})
	if result.Error != nil {
		t.Fatalf("pipeline failed: %v", result.Error)
	}
	if !result.StepExecuted("set-target") {
		t.Error("set-target step should have executed")
	}
	if !result.StepExecuted("destroy") {
		t.Error("destroy step should have executed")
	}
	if !result.StepExecuted("confirm") {
		t.Error("confirm step should have executed")
	}
	if destroyRec.CallCount() != 1 {
		t.Errorf("expected 1 call to step.aws_destroy, got %d", destroyRec.CallCount())
	}

	out := result.StepOutput("destroy")
	if out["status"] != "deleted" {
		t.Errorf("expected status=deleted, got %v", out["status"])
	}

	if result.Output["teardown_complete"] != "true" {
		t.Errorf("expected teardown_complete=true, got %v", result.Output["teardown_complete"])
	}
}

// TestIntegration_AWSMultiResourcePipeline tests provisioning multiple AWS
// resource types (VPC, RDS, ECS) sequentially in a single pipeline.
func TestIntegration_AWSMultiResourcePipeline(t *testing.T) {
	vpcRec := wftest.RecordStep("step.aws_provision_vpc")
	vpcRec.WithOutput(map[string]any{
		"vpc_id": "vpc-0abc1234def56789",
		"status": "created",
		"cidr":   "10.0.0.0/16",
	})

	dbRec := wftest.RecordStep("step.aws_provision_rds")
	dbRec.WithOutput(map[string]any{
		"db_instance_id": "myapp-db",
		"endpoint":       "myapp-db.abc123.us-east-1.rds.amazonaws.com",
		"status":         "available",
	})

	ecsRec := wftest.RecordStep("step.aws_provision_ecs")
	ecsRec.WithOutput(map[string]any{
		"cluster_arn": "arn:aws:ecs:us-east-1:123456789:cluster/myapp",
		"service_arn": "arn:aws:ecs:us-east-1:123456789:service/myapp/api",
		"status":      "created",
	})

	h := wftest.New(t,
		wftest.WithYAML(`
pipelines:
  provision-full-stack:
    steps:
      - name: config
        type: step.set
        config:
          values:
            region: us-east-1
            env: production
            app: myapp
      - name: provision-vpc
        type: step.aws_provision_vpc
        config:
          cidr: 10.0.0.0/16
          region: us-east-1
      - name: provision-rds
        type: step.aws_provision_rds
        config:
          instance_class: db.t3.medium
          engine: postgres
          region: us-east-1
      - name: provision-ecs
        type: step.aws_provision_ecs
        config:
          cluster_name: myapp
          region: us-east-1
      - name: summarize
        type: step.set
        config:
          values:
            provisioned: "true"
`),
		vpcRec,
		dbRec,
		ecsRec,
	)

	result := h.ExecutePipeline("provision-full-stack", nil)
	if result.Error != nil {
		t.Fatalf("pipeline failed: %v", result.Error)
	}

	// All steps should have executed.
	for _, step := range []string{"config", "provision-vpc", "provision-rds", "provision-ecs", "summarize"} {
		if !result.StepExecuted(step) {
			t.Errorf("step %q should have executed", step)
		}
	}

	// Each resource step called exactly once.
	if vpcRec.CallCount() != 1 {
		t.Errorf("expected 1 VPC provision call, got %d", vpcRec.CallCount())
	}
	if dbRec.CallCount() != 1 {
		t.Errorf("expected 1 RDS provision call, got %d", dbRec.CallCount())
	}
	if ecsRec.CallCount() != 1 {
		t.Errorf("expected 1 ECS provision call, got %d", ecsRec.CallCount())
	}

	// Verify step outputs.
	vpcOut := result.StepOutput("provision-vpc")
	if vpcOut["vpc_id"] != "vpc-0abc1234def56789" {
		t.Errorf("unexpected vpc_id: %v", vpcOut["vpc_id"])
	}

	dbOut := result.StepOutput("provision-rds")
	if dbOut["status"] != "available" {
		t.Errorf("expected RDS status=available, got %v", dbOut["status"])
	}

	ecsOut := result.StepOutput("provision-ecs")
	if ecsOut["status"] != "created" {
		t.Errorf("expected ECS status=created, got %v", ecsOut["status"])
	}

	// Final pipeline output should include the summarize step result.
	if result.Output["provisioned"] != "true" {
		t.Errorf("expected provisioned=true, got %v", result.Output["provisioned"])
	}

	// Step count: 5 steps total.
	if result.StepCount() != 5 {
		t.Errorf("expected 5 steps executed, got %d", result.StepCount())
	}
}

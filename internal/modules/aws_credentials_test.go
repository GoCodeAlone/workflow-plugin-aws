package modules

import (
	"context"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/credref"
)

func TestAWSCredentialsProvider_ModuleTypes(t *testing.T) {
	p := NewAWSCredentialsProvider()
	types := p.ModuleTypes()
	if len(types) != 1 || types[0] != "aws.credentials" {
		t.Errorf("ModuleTypes = %v, want [aws.credentials]", types)
	}
}

func TestAWSCredentialsProvider_CreateModule_RegistersCredentials(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewAWSCredentialsProvider()

	cfg := map[string]any{
		"region": "us-east-1",
		"credentials": map[string]any{
			"type":         "static",
			"accessKey":    "AKID",
			"secretKey":    "SECRET",
			"sessionToken": "TOKEN",
		},
	}
	inst, err := p.CreateModule("aws.credentials", "default-creds", cfg)
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if inst == nil {
		t.Fatal("CreateModule returned nil instance")
	}

	got, ok := credref.Resolve("default-creds")
	if !ok {
		t.Fatal("credref.Resolve(default-creds): not found")
	}
	if got.Source != "static" {
		t.Errorf("Source = %q, want static", got.Source)
	}
	if got.AccessKey != "AKID" || got.SecretKey != "SECRET" || got.SessionToken != "TOKEN" {
		t.Errorf("creds = %q/%q/%q, want AKID/SECRET/TOKEN", got.AccessKey, got.SecretKey, got.SessionToken)
	}
	if got.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", got.Region)
	}
}

func TestAWSCredentialsProvider_CreateModule_ProfileType(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewAWSCredentialsProvider()
	cfg := map[string]any{
		"credentials": map[string]any{
			"type":    "profile",
			"profile": "dev",
		},
	}
	if _, err := p.CreateModule("aws.credentials", "p", cfg); err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	got, _ := credref.Resolve("p")
	if got.Source != "profile" || got.Profile != "dev" {
		t.Errorf("Source/Profile = %q/%q, want profile/dev", got.Source, got.Profile)
	}
}

func TestAWSCredentialsProvider_CreateModule_RoleARNType(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewAWSCredentialsProvider()
	cfg := map[string]any{
		"credentials": map[string]any{
			"type":        "role_arn",
			"roleArn":     "arn:aws:iam::123456789012:role/r",
			"externalId":  "ext",
			"sessionName": "s",
		},
	}
	if _, err := p.CreateModule("aws.credentials", "r", cfg); err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	got, _ := credref.Resolve("r")
	if got.Source != "role_arn" {
		t.Errorf("Source = %q, want role_arn", got.Source)
	}
	if got.RoleARN != "arn:aws:iam::123456789012:role/r" || got.ExternalID != "ext" || got.SessionName != "s" {
		t.Errorf("role fields = %q/%q/%q, want arn:.../ext/s", got.RoleARN, got.ExternalID, got.SessionName)
	}
}

func TestAWSCredentialsProvider_CreateModule_DuplicateNameErrors(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewAWSCredentialsProvider()
	cfg := map[string]any{"credentials": map[string]any{"type": "static"}}
	if _, err := p.CreateModule("aws.credentials", "dup", cfg); err != nil {
		t.Fatalf("first CreateModule: %v", err)
	}
	if _, err := p.CreateModule("aws.credentials", "dup", cfg); err == nil {
		t.Fatal("expected duplicate-name error on second CreateModule with same name")
	}
}

func TestAWSCredentialsInstance_LifecycleIsNoOp(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewAWSCredentialsProvider()
	inst, err := p.CreateModule("aws.credentials", "lifecycle", map[string]any{})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if err := inst.Init(); err != nil {
		t.Errorf("Init: %v", err)
	}
	if err := inst.Start(context.Background()); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := inst.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

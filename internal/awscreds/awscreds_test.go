package awscreds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

func TestBuildAWSConfig_Static(t *testing.T) {
	ctx := context.Background()
	cfg, err := BuildAWSConfig(ctx, CredInput{
		Source:    "static",
		Region:    "us-west-2",
		AccessKey: "AKIDTESTSTATIC",
		SecretKey: "SECRETTESTSTATIC",
	})
	if err != nil {
		t.Fatalf("BuildAWSConfig(static): %v", err)
	}
	if cfg.Region != "us-west-2" {
		t.Errorf("Region = %q, want us-west-2", cfg.Region)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		t.Fatalf("Credentials.Retrieve: %v", err)
	}
	if creds.AccessKeyID != "AKIDTESTSTATIC" || creds.SecretAccessKey != "SECRETTESTSTATIC" {
		t.Errorf("retrieved creds = %q/%q, want AKIDTESTSTATIC/SECRETTESTSTATIC",
			creds.AccessKeyID, creds.SecretAccessKey)
	}
}

func TestBuildAWSConfig_EmptySourceUsesDefaultChain(t *testing.T) {
	// Isolate the env so the default chain doesn't pick up ambient credentials
	// (which would make the test order-dependent and noisy).
	isolateAWSEnv(t)
	ctx := context.Background()
	cfg, err := BuildAWSConfig(ctx, CredInput{Region: "us-east-1"})
	if err != nil {
		t.Fatalf("BuildAWSConfig(default chain): %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", cfg.Region)
	}
	// LoadDefaultConfig itself must not error when there are no creds — the
	// chain defers actual credential resolution to Retrieve() time.
}

func TestBuildAWSConfig_Profile(t *testing.T) {
	isolateAWSEnv(t)
	// Write a throwaway shared-config + shared-credentials file and point
	// AWS_CONFIG_FILE / AWS_SHARED_CREDENTIALS_FILE at them.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(configPath, []byte("[profile dev]\nregion = us-east-2\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(credsPath, []byte("[dev]\naws_access_key_id = AKIDTESTPROFILE\naws_secret_access_key = SECRETTESTPROFILE\n"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)

	ctx := context.Background()
	cfg, err := BuildAWSConfig(ctx, CredInput{Source: "profile", Profile: "dev"})
	if err != nil {
		t.Fatalf("BuildAWSConfig(profile): %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		t.Fatalf("Credentials.Retrieve: %v", err)
	}
	if creds.AccessKeyID != "AKIDTESTPROFILE" {
		t.Errorf("profile-loaded AccessKeyID = %q, want AKIDTESTPROFILE", creds.AccessKeyID)
	}
}

func TestBuildAWSConfig_RoleARN_InjectedSTS(t *testing.T) {
	// Inject a fake STS client so AssumeRole is exercised without a network
	// call. Restore the global after the test.
	origNew := newSTSClient
	t.Cleanup(func() { newSTSClient = origNew })

	var captured *sts.AssumeRoleInput
	fake := &fakeSTS{
		assume: func(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			captured = in
			return &sts.AssumeRoleOutput{
				Credentials: &ststypes.Credentials{
					AccessKeyId:     aws.String("AKIDASSUMED"),
					SecretAccessKey: aws.String("SECRETASSUMED"),
					SessionToken:    aws.String("TOKENASSUMED"),
				},
			}, nil
		},
	}
	newSTSClient = func(_ aws.Config) stsAssumeRoleAPI { return fake }

	ctx := context.Background()
	cfg, err := BuildAWSConfig(ctx, CredInput{
		Source:      "role_arn",
		Region:      "us-east-1",
		RoleARN:     "arn:aws:iam::123456789012:role/test-role",
		ExternalID:  "ext-123",
		SessionName: "test-session",
		AccessKey:   "AKIDBASE",
		SecretKey:   "SECRETBASE",
	})
	if err != nil {
		t.Fatalf("BuildAWSConfig(role_arn): %v", err)
	}
	if captured == nil {
		t.Fatal("AssumeRole was not called")
	}
	if aws.ToString(captured.RoleArn) != "arn:aws:iam::123456789012:role/test-role" {
		t.Errorf("RoleArn = %q, want arn:aws:iam::123456789012:role/test-role", aws.ToString(captured.RoleArn))
	}
	if aws.ToString(captured.RoleSessionName) != "test-session" {
		t.Errorf("RoleSessionName = %q, want test-session", aws.ToString(captured.RoleSessionName))
	}
	if aws.ToString(captured.ExternalId) != "ext-123" {
		t.Errorf("ExternalId = %q, want ext-123", aws.ToString(captured.ExternalId))
	}
	got, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		t.Fatalf("Retrieve assumed creds: %v", err)
	}
	if got.AccessKeyID != "AKIDASSUMED" || got.SecretAccessKey != "SECRETASSUMED" || got.SessionToken != "TOKENASSUMED" {
		t.Errorf("assumed creds = %q/%q/%q, want AKIDASSUMED/SECRETASSUMED/TOKENASSUMED",
			got.AccessKeyID, got.SecretAccessKey, got.SessionToken)
	}
}

func TestBuildAWSConfig_RoleARN_DefaultSessionName(t *testing.T) {
	origNew := newSTSClient
	t.Cleanup(func() { newSTSClient = origNew })

	var captured *sts.AssumeRoleInput
	newSTSClient = func(_ aws.Config) stsAssumeRoleAPI {
		return &fakeSTS{
			assume: func(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
				captured = in
				return &sts.AssumeRoleOutput{
					Credentials: &ststypes.Credentials{
						AccessKeyId:     aws.String("AKID"),
						SecretAccessKey: aws.String("SECRET"),
					},
				}, nil
			},
		}
	}

	_, err := BuildAWSConfig(context.Background(), CredInput{
		Source:  "role_arn",
		RoleARN: "arn:aws:iam::000000000000:role/r",
	})
	if err != nil {
		t.Fatalf("BuildAWSConfig: %v", err)
	}
	if aws.ToString(captured.RoleSessionName) != "workflow-session" {
		t.Errorf("default RoleSessionName = %q, want workflow-session", aws.ToString(captured.RoleSessionName))
	}
}

func TestBuildAWSConfig_RoleARN_EmptyARNErrors(t *testing.T) {
	_, err := BuildAWSConfig(context.Background(), CredInput{Source: "role_arn"})
	if err == nil {
		t.Fatal("expected error when role_arn source has empty RoleARN")
	}
}

func TestBuildAWSConfig_RoleARN_AssumeRoleErrorPropagates(t *testing.T) {
	origNew := newSTSClient
	t.Cleanup(func() { newSTSClient = origNew })
	newSTSClient = func(_ aws.Config) stsAssumeRoleAPI {
		return &fakeSTS{
			assume: func(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
				return nil, fmt.Errorf("simulated STS denial")
			},
		}
	}

	_, err := BuildAWSConfig(context.Background(), CredInput{
		Source:  "role_arn",
		RoleARN: "arn:aws:iam::000000000000:role/r",
	})
	if err == nil {
		t.Fatal("expected AssumeRole error to propagate")
	}
}

// isolateAWSEnv clears AWS credential env vars + config-file overrides so
// tests don't inadvertently pick up developer / CI ambient credentials.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
	} {
		t.Setenv(v, "")
	}
	// Point HOME at an empty dir so a real ~/.aws/{config,credentials} is
	// not consulted on the host running the tests.
	t.Setenv("HOME", t.TempDir())
}

type fakeSTS struct {
	assume func(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

func (f *fakeSTS) AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return f.assume(ctx, in, opts...)
}

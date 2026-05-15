// Package awscreds provides the in-plugin AWS credential resolution path.
//
// BuildAWSConfig is the single entry point: given a CredInput (parsed from
// either a YAML `credentials:` block or a host-delivered CloudCredentials),
// it returns a fully-resolved aws.Config. The 4 source paths are:
//
//   - "static": inline AccessKey/SecretKey/SessionToken;
//   - "env" (or unset): aws-sdk-go-v2's default credential chain;
//   - "profile": shared-config profile via config.WithSharedConfigProfile;
//   - "role_arn": STS AssumeRole with optional ExternalID + base creds.
//
// The "profile" and "role_arn" SDK blocks are ported from workflow core's
// module/cloud_account_aws_creds.go (awsProfileResolver, awsRoleARNResolver),
// which Phase-B PR 4 (plan-2 Task 13) rewrites to *declare, don't resolve*.
// The SDK-bearing resolution lives here, in the plugin.
//
// CredInput.Source MUST be populated by the call-site from the YAML
// `credentials.type` field (the value the user wrote in their config). It
// is NOT read from CloudAccount.Extra — that map never crosses the
// host↔plugin gRPC boundary.
package awscreds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// CredInput is the parsed-config shape BuildAWSConfig consumes. The call-site
// (a Provider's CreateModule/CreateStep or the IaC provider's Initialize)
// parses the `credentials:` YAML block — or the legacy top-level
// access_key_id/secret_access_key keys — into this struct.
type CredInput struct {
	AccessKey    string
	SecretKey    string
	SessionToken string
	Region       string
	RoleARN      string
	ExternalID   string
	Profile      string
	// Source mirrors the YAML `credentials.type` field — one of
	// "static" | "env" | "profile" | "role_arn" | "" (default chain).
	Source string
	// SessionName is the STS AssumeRole session name. Defaults to
	// "workflow-session" when empty. Honoured only when Source == "role_arn".
	SessionName string
}

// stsAssumeRoleAPI is the subset of *sts.Client BuildAWSConfig calls. It
// exists so tests can inject a fake STS implementation without spinning up
// a real STS endpoint.
type stsAssumeRoleAPI interface {
	AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// newSTSClient builds an STS client from the given base aws.Config. Tests
// override this var to inject a fake STS API.
var newSTSClient = func(cfg aws.Config) stsAssumeRoleAPI {
	return sts.NewFromConfig(cfg)
}

// BuildAWSConfig returns a resolved aws.Config for the given CredInput.
// See package doc for the source-path semantics.
func BuildAWSConfig(ctx context.Context, c CredInput) (aws.Config, error) {
	switch c.Source {
	case "profile":
		return loadProfile(ctx, c)
	case "role_arn":
		return loadRoleARN(ctx, c)
	}
	// "static" | "env" | "" — all flow through the default chain with
	// optional static-credential override when both keys are supplied.
	opts := baseLoadOptions(c)
	if c.AccessKey != "" && c.SecretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, c.SessionToken),
		))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws creds: load default config: %w", err)
	}
	return cfg, nil
}

// loadProfile loads aws.Config from a named shared-config profile. Ported
// from workflow core's awsProfileResolver (cloud_account_aws_creds.go).
func loadProfile(ctx context.Context, c CredInput) (aws.Config, error) {
	profile := c.Profile
	if profile == "" {
		profile = "default"
	}
	opts := baseLoadOptions(c)
	opts = append(opts, config.WithSharedConfigProfile(profile))
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws creds: load profile %q: %w", profile, err)
	}
	return cfg, nil
}

// loadRoleARN obtains temporary credentials via STS AssumeRole on top of
// base credentials (inline static keys when supplied, otherwise the default
// chain). Ported from workflow core's awsRoleARNResolver
// (cloud_account_aws_creds.go).
func loadRoleARN(ctx context.Context, c CredInput) (aws.Config, error) {
	if c.RoleARN == "" {
		return aws.Config{}, fmt.Errorf("aws creds: role_arn source requires non-empty RoleARN")
	}
	baseOpts := baseLoadOptions(c)
	if c.AccessKey != "" && c.SecretKey != "" {
		baseOpts = append(baseOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, c.SessionToken),
		))
	}
	baseCfg, err := config.LoadDefaultConfig(ctx, baseOpts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws creds: load base config for role_arn: %w", err)
	}

	sessionName := c.SessionName
	if sessionName == "" {
		sessionName = "workflow-session"
	}
	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(c.RoleARN),
		RoleSessionName: aws.String(sessionName),
	}
	if c.ExternalID != "" {
		input.ExternalId = aws.String(c.ExternalID)
	}

	out, err := newSTSClient(baseCfg).AssumeRole(ctx, input)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws creds: AssumeRole %q: %w", c.RoleARN, err)
	}
	if out == nil || out.Credentials == nil {
		return aws.Config{}, fmt.Errorf("aws creds: AssumeRole %q returned no credentials", c.RoleARN)
	}

	assumed := baseCfg.Copy()
	assumed.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(out.Credentials.AccessKeyId),
		aws.ToString(out.Credentials.SecretAccessKey),
		aws.ToString(out.Credentials.SessionToken),
	)
	return assumed, nil
}

// baseLoadOptions builds the LoadDefaultConfig options common to every
// path — currently just Region when set.
func baseLoadOptions(c CredInput) []func(*config.LoadOptions) error {
	var opts []func(*config.LoadOptions) error
	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}
	return opts
}

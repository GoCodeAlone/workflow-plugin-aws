package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// S3Client is the subset of S3 API used by S3Driver.
type S3Client interface {
	CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
	GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
	PutBucketVersioning(ctx context.Context, params *s3.PutBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error)
	PutBucketEncryption(ctx context.Context, params *s3.PutBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error)
}

// S3Driver manages S3 buckets (infra.storage).
type S3Driver struct {
	noSensitiveKeys
	client S3Client
	region string
}

// NewS3Driver creates an S3 driver from an AWS config.
func NewS3Driver(cfg awssdk.Config, region string) *S3Driver {
	return &S3Driver{client: s3.NewFromConfig(cfg), region: region}
}

// NewS3DriverWithClient creates an S3 driver with a custom client (for tests).
func NewS3DriverWithClient(client S3Client, region string) *S3Driver {
	return &S3Driver{client: client, region: region}
}

func (d *S3Driver) ResourceType() string { return "infra.storage" }

func (d *S3Driver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	in := &s3.CreateBucketInput{
		Bucket: awssdk.String(spec.Name),
	}
	// us-east-1 does not accept a LocationConstraint
	if d.region != "" && d.region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(d.region),
		}
	}

	_, err := d.client.CreateBucket(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("s3: create bucket %q: %w", spec.Name, err)
	}

	// Enable versioning if requested
	if boolProp(spec.Config, "versioning", false) {
		_, err = d.client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: awssdk.String(spec.Name),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatusEnabled,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("s3: enable versioning %q: %w", spec.Name, err)
		}
	}

	// Enable encryption if requested
	if boolProp(spec.Config, "encryption", true) {
		_, err = d.client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
			Bucket: awssdk.String(spec.Name),
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{
					{
						ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
							SSEAlgorithm: s3types.ServerSideEncryptionAes256,
						},
					},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("s3: enable encryption %q: %w", spec.Name, err)
		}
	}

	return s3BucketToOutput(spec.Name, d.region), nil
}

func (d *S3Driver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	_, err := d.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: awssdk.String(ref.Name)})
	if err != nil {
		return nil, fmt.Errorf("s3: head bucket %q: %w", ref.Name, err)
	}

	locOut, err := d.client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: awssdk.String(ref.Name)})
	region := d.region
	if err == nil {
		region = string(locOut.LocationConstraint)
		if region == "" {
			region = "us-east-1"
		}
	}

	return s3BucketToOutput(ref.Name, region), nil
}

func (d *S3Driver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if boolProp(spec.Config, "versioning", false) {
		_, err := d.client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: awssdk.String(ref.Name),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatusEnabled,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("s3: update versioning %q: %w", ref.Name, err)
		}
	}
	return d.Read(ctx, ref)
}

func (d *S3Driver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	_, err := d.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: awssdk.String(ref.Name)})
	if err != nil {
		return fmt.Errorf("s3: delete bucket %q: %w", ref.Name, err)
	}
	return nil
}

func (d *S3Driver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *S3Driver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "bucket exists"}, nil
}

func (d *S3Driver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("s3: buckets scale automatically")
}

func s3BucketToOutput(name, region string) *interfaces.ResourceOutput {
	endpoint := fmt.Sprintf("https://%s.s3.%s.amazonaws.com", name, region)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.storage",
		ProviderID: name,
		Outputs: map[string]any{
			"bucket_name": name,
			"region":      region,
			"endpoint":    endpoint,
		},
		Status: "running",
	}
}

var _ interfaces.ResourceDriver = (*S3Driver)(nil)

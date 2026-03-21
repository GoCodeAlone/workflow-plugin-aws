package drivers_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockS3Client struct {
	createErr     error
	headErr       error
	deleteErr     error
	locationOut   *s3.GetBucketLocationOutput
	locationErr   error
	versioningErr error
	encryptionErr error
}

func (m *mockS3Client) CreateBucket(_ context.Context, _ *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	return &s3.CreateBucketOutput{}, m.createErr
}
func (m *mockS3Client) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, m.headErr
}
func (m *mockS3Client) DeleteBucket(_ context.Context, _ *s3.DeleteBucketInput, _ ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	return &s3.DeleteBucketOutput{}, m.deleteErr
}
func (m *mockS3Client) GetBucketLocation(_ context.Context, _ *s3.GetBucketLocationInput, _ ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return m.locationOut, m.locationErr
}
func (m *mockS3Client) PutBucketVersioning(_ context.Context, _ *s3.PutBucketVersioningInput, _ ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error) {
	return &s3.PutBucketVersioningOutput{}, m.versioningErr
}
func (m *mockS3Client) PutBucketEncryption(_ context.Context, _ *s3.PutBucketEncryptionInput, _ ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error) {
	return &s3.PutBucketEncryptionOutput{}, m.encryptionErr
}

func TestS3Driver_Create(t *testing.T) {
	d := drivers.NewS3DriverWithClient(&mockS3Client{}, "us-east-1")
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-bucket",
		Config: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.storage" {
		t.Errorf("expected infra.storage, got %s", out.Type)
	}
	if out.ProviderID != "my-bucket" {
		t.Errorf("expected ProviderID my-bucket, got %s", out.ProviderID)
	}
}

func TestS3Driver_Create_WithVersioningAndEncryption(t *testing.T) {
	d := drivers.NewS3DriverWithClient(&mockS3Client{}, "us-west-2")
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name: "my-bucket",
		Config: map[string]any{
			"versioning": true,
			"encryption": true,
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Outputs["region"] != "us-west-2" {
		t.Errorf("expected us-west-2, got %v", out.Outputs["region"])
	}
}

func TestS3Driver_Read(t *testing.T) {
	mock := &mockS3Client{
		locationOut: &s3.GetBucketLocationOutput{
			LocationConstraint: s3types.BucketLocationConstraint("us-east-1"),
		},
	}
	d := drivers.NewS3DriverWithClient(mock, "us-east-1")
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-bucket"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-bucket" {
		t.Errorf("expected my-bucket, got %s", out.Name)
	}
}

func TestS3Driver_Delete(t *testing.T) {
	d := drivers.NewS3DriverWithClient(&mockS3Client{}, "us-east-1")
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-bucket"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestS3Driver_HealthCheck(t *testing.T) {
	mock := &mockS3Client{
		locationOut: &s3.GetBucketLocationOutput{
			LocationConstraint: s3types.BucketLocationConstraint("us-east-1"),
		},
	}
	d := drivers.NewS3DriverWithClient(mock, "us-east-1")
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy")
	}
}

func TestS3Driver_Scale_ReturnsError(t *testing.T) {
	d := drivers.NewS3DriverWithClient(&mockS3Client{}, "us-east-1")
	_, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-bucket"}, 3)
	if err == nil {
		t.Error("expected error from Scale on S3 bucket")
	}
}

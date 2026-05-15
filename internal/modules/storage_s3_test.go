package modules

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/credref"
)

func TestS3StorageProvider_ModuleTypes(t *testing.T) {
	p := NewS3StorageProvider()
	if got := p.ModuleTypes(); len(got) != 1 || got[0] != "storage.s3" {
		t.Errorf("ModuleTypes = %v, want [storage.s3]", got)
	}
}

func TestS3StorageProvider_CreateModule_RequiresBucket(t *testing.T) {
	p := NewS3StorageProvider()
	if _, err := p.CreateModule("storage.s3", "nobucket", map[string]any{}); err == nil {
		t.Fatal("expected error when bucket is missing")
	}
}

func TestS3StorageProvider_CreateModule_InlineCredentials(t *testing.T) {
	p := NewS3StorageProvider()
	cfg := map[string]any{
		"bucket":   "b1",
		"region":   "us-east-2",
		"endpoint": "https://minio.local",
		"credentials": map[string]any{
			"type":      "static",
			"accessKey": "AKID",
			"secretKey": "SECRET",
		},
	}
	inst, err := p.CreateModule("storage.s3", "inline", cfg)
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	m, ok := inst.(*s3StorageInstance)
	if !ok {
		t.Fatalf("CreateModule returned %T, want *s3StorageInstance", inst)
	}
	if m.bucket != "b1" || m.region != "us-east-2" || m.endpoint != "https://minio.local" {
		t.Errorf("fields = %q/%q/%q, want b1/us-east-2/https://minio.local",
			m.bucket, m.region, m.endpoint)
	}
	if m.cred.Source != "static" || m.cred.AccessKey != "AKID" || m.cred.SecretKey != "SECRET" {
		t.Errorf("cred = %+v, want static/AKID/SECRET", m.cred)
	}
	if m.cred.Region != "us-east-2" {
		t.Errorf("cred.Region = %q, want us-east-2 (module region propagates)", m.cred.Region)
	}
}

func TestS3StorageProvider_CreateModule_CredentialsRef(t *testing.T) {
	t.Cleanup(credref.Reset)
	want := awscreds.CredInput{
		AccessKey: "AKIDREF",
		SecretKey: "SECRETREF",
		Source:    "static",
	}
	if err := credref.Register("shared-creds", want); err != nil {
		t.Fatalf("Register: %v", err)
	}

	p := NewS3StorageProvider()
	cfg := map[string]any{
		"bucket":          "b2",
		"region":          "us-west-2",
		"credentials_ref": "shared-creds",
	}
	inst, err := p.CreateModule("storage.s3", "ref", cfg)
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	m := inst.(*s3StorageInstance)
	if m.cred.AccessKey != "AKIDREF" || m.cred.SecretKey != "SECRETREF" {
		t.Errorf("resolved cred keys = %q/%q, want AKIDREF/SECRETREF",
			m.cred.AccessKey, m.cred.SecretKey)
	}
	if m.cred.Region != "us-west-2" {
		t.Errorf("cred.Region = %q, want us-west-2 (module region overrides ref's region)", m.cred.Region)
	}
}

func TestS3StorageProvider_CreateModule_CredentialsRef_MissingErrors(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewS3StorageProvider()
	cfg := map[string]any{
		"bucket":          "b3",
		"credentials_ref": "does-not-exist",
	}
	_, err := p.CreateModule("storage.s3", "missing", cfg)
	if err == nil {
		t.Fatal("expected error when credentials_ref is unregistered")
	}
	if !strings.Contains(err.Error(), "credentials_ref \"does-not-exist\" not found") {
		t.Errorf("error = %q, expected to mention the missing ref name", err)
	}
}

func TestS3StorageProvider_CreateModule_InlineBeatsRef(t *testing.T) {
	t.Cleanup(credref.Reset)
	_ = credref.Register("would-lose", awscreds.CredInput{AccessKey: "REFSIDE"})
	p := NewS3StorageProvider()
	cfg := map[string]any{
		"bucket": "b",
		"credentials": map[string]any{
			"type":      "static",
			"accessKey": "INLINESIDE",
			"secretKey": "x",
		},
		"credentials_ref": "would-lose",
	}
	inst, err := p.CreateModule("storage.s3", "both", cfg)
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	m := inst.(*s3StorageInstance)
	if m.cred.AccessKey != "INLINESIDE" {
		t.Errorf("AccessKey = %q, want INLINESIDE (inline beats credentials_ref)", m.cred.AccessKey)
	}
}

func TestS3StorageProvider_CreateModule_NoCredsDefaultChain(t *testing.T) {
	p := NewS3StorageProvider()
	inst, err := p.CreateModule("storage.s3", "default", map[string]any{
		"bucket": "b",
		"region": "us-east-1",
	})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	m := inst.(*s3StorageInstance)
	if m.cred.Source != "" || m.cred.AccessKey != "" {
		t.Errorf("expected zero CredInput (default chain), got %+v", m.cred)
	}
	if m.cred.Region != "us-east-1" {
		t.Errorf("cred.Region = %q, want us-east-1", m.cred.Region)
	}
}

// ── Lifecycle + Storage-operation tests (via injected mock client) ──────────

func TestS3StorageInstance_Lifecycle_TestSeam(t *testing.T) {
	p := NewS3StorageProvider()
	inst, _ := p.CreateModule("storage.s3", "lifecycle", map[string]any{"bucket": "b"})
	m := inst.(*s3StorageInstance)
	m.SetTestClient(newMockS3())

	if err := m.Init(); err != nil {
		t.Errorf("Init: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestS3StorageInstance_StorageOperations_RoundTrip(t *testing.T) {
	p := NewS3StorageProvider()
	inst, _ := p.CreateModule("storage.s3", "ops", map[string]any{"bucket": "test-bucket"})
	m := inst.(*s3StorageInstance)
	mock := newMockS3()
	m.SetTestClient(mock)
	_ = m.Start(context.Background())
	ctx := context.Background()

	// Put.
	if err := m.PutObject(ctx, "k1", bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if mock.bucket != "test-bucket" {
		t.Errorf("mock bucket = %q, want test-bucket", mock.bucket)
	}
	// Get.
	r, err := m.GetObject(ctx, "k1")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if string(got) != "hello" {
		t.Errorf("GetObject returned %q, want hello", got)
	}
	// Delete.
	if err := m.DeleteObject(ctx, "k1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, err := m.GetObject(ctx, "k1"); err == nil {
		t.Error("GetObject after Delete: expected error")
	}
}

func TestS3StorageInstance_OperationsBeforeStartError(t *testing.T) {
	p := NewS3StorageProvider()
	inst, _ := p.CreateModule("storage.s3", "no-start", map[string]any{"bucket": "b"})
	m := inst.(*s3StorageInstance)
	if err := m.PutObject(context.Background(), "k", bytes.NewReader(nil)); err == nil {
		t.Error("PutObject without Start: expected error")
	}
	if _, err := m.GetObject(context.Background(), "k"); err == nil {
		t.Error("GetObject without Start: expected error")
	}
	if err := m.DeleteObject(context.Background(), "k"); err == nil {
		t.Error("DeleteObject without Start: expected error")
	}
}

// mockS3 is an in-memory s3API for tests.
type mockS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	bucket  string
}

func newMockS3() *mockS3 {
	return &mockS3{objects: make(map[string][]byte)}
}

func (m *mockS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bucket = aws.ToString(in.Bucket)
	key := aws.ToString(in.Key)
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	m.objects[key] = data
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := aws.ToString(in.Key)
	data, ok := m.objects[key]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (m *mockS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

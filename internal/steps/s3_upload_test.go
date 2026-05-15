package steps

import (
	"bytes"
	"context"
	"encoding/base64"
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

func TestS3UploadStepProvider_StepTypes(t *testing.T) {
	p := NewS3UploadStepProvider()
	if got := p.StepTypes(); len(got) != 1 || got[0] != "step.s3_upload" {
		t.Errorf("StepTypes = %v, want [step.s3_upload]", got)
	}
}

func TestS3UploadStepProvider_CreateStep_RequiredFields(t *testing.T) {
	p := NewS3UploadStepProvider()
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"missing bucket", map[string]any{"region": "r", "key": "k", "body_from": "b"}, "'bucket' is required"},
		{"missing region", map[string]any{"bucket": "b", "key": "k", "body_from": "b"}, "'region' is required"},
		{"missing key", map[string]any{"bucket": "b", "region": "r", "body_from": "b"}, "'key' is required"},
		{"missing body_from", map[string]any{"bucket": "b", "region": "r", "key": "k"}, "'body_from' is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.CreateStep("step.s3_upload", "x", tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestS3UploadStep_Execute_RoundTrip(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, err := p.CreateStep("step.s3_upload", "upload", map[string]any{
		"bucket":    "test-bucket",
		"region":    "us-east-1",
		"key":       "uploads/{{.user_id}}/avatar.png",
		"body_from": "steps.encode.payload",
	})
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	step := stepIface.(*s3UploadStep)
	mock := newMockPutObject()
	step.SetTestClient(mock)

	body := []byte("PNGDATA\x89\x00")
	encoded := base64.StdEncoding.EncodeToString(body)

	res, err := step.Execute(
		context.Background(),
		nil,
		map[string]map[string]any{"encode": {"payload": encoded}},
		map[string]any{"user_id": "u42"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Output == nil {
		t.Fatal("nil StepResult / Output")
	}
	if got := res.Output["key"]; got != "uploads/u42/avatar.png" {
		t.Errorf("Output[key] = %v, want uploads/u42/avatar.png", got)
	}
	if got := res.Output["bucket"]; got != "test-bucket" {
		t.Errorf("Output[bucket] = %v, want test-bucket", got)
	}
	wantURL := "https://test-bucket.s3.us-east-1.amazonaws.com/uploads/u42/avatar.png"
	if got := res.Output["url"]; got != wantURL {
		t.Errorf("Output[url] = %v, want %s", got, wantURL)
	}

	// Verify the mock saw the right PutObject input.
	in := mock.last
	if in == nil {
		t.Fatal("PutObject was not called")
	}
	if aws.ToString(in.Bucket) != "test-bucket" || aws.ToString(in.Key) != "uploads/u42/avatar.png" {
		t.Errorf("PutObject bucket/key = %q/%q, want test-bucket/uploads/u42/avatar.png",
			aws.ToString(in.Bucket), aws.ToString(in.Key))
	}
	gotBody, _ := io.ReadAll(in.Body)
	if !bytes.Equal(gotBody, body) {
		t.Errorf("PutObject body = %q, want %q (decoded)", gotBody, body)
	}
}

func TestS3UploadStep_Execute_BodyFromMissing(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r", "key": "k", "body_from": "steps.nope.payload",
	})
	step := stepIface.(*s3UploadStep)
	step.SetTestClient(newMockPutObject())

	_, err := step.Execute(context.Background(), nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "body_from") {
		t.Errorf("err = %v, want body_from error", err)
	}
}

func TestS3UploadStep_Execute_BodyFromBadBase64(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r", "key": "k", "body_from": "payload",
	})
	step := stepIface.(*s3UploadStep)
	step.SetTestClient(newMockPutObject())

	_, err := step.Execute(context.Background(), nil, nil, map[string]any{"payload": "***not base64***"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("err = %v, want base64 error", err)
	}
}

func TestS3UploadStep_Execute_PutObjectErrorPropagates(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r", "key": "k", "body_from": "payload",
	})
	step := stepIface.(*s3UploadStep)
	step.SetTestClient(&mockPutObject{err: errors.New("simulated AccessDenied")})

	_, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"payload": base64.StdEncoding.EncodeToString([]byte("data")),
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "simulated AccessDenied") {
		t.Errorf("err = %v, want propagated PutObject error", err)
	}
}

func TestS3UploadStep_Execute_ContentTypeFrom(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket":            "b",
		"region":            "r",
		"key":               "k",
		"body_from":         "payload",
		"content_type":      "application/octet-stream",
		"content_type_from": "steps.detect.mime",
	})
	step := stepIface.(*s3UploadStep)
	mock := newMockPutObject()
	step.SetTestClient(mock)

	_, err := step.Execute(context.Background(), nil,
		map[string]map[string]any{"detect": {"mime": "image/png"}},
		map[string]any{"payload": base64.StdEncoding.EncodeToString([]byte("x"))},
		nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := aws.ToString(mock.last.ContentType); got != "image/png" {
		t.Errorf("ContentType = %q, want image/png (content_type_from beats content_type)", got)
	}
}

func TestS3UploadStep_Execute_ContentTypeFromMissingFallsBack(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket":            "b",
		"region":            "r",
		"key":               "k",
		"body_from":         "payload",
		"content_type":      "application/octet-stream",
		"content_type_from": "nope.path",
	})
	step := stepIface.(*s3UploadStep)
	mock := newMockPutObject()
	step.SetTestClient(mock)

	_, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"payload": base64.StdEncoding.EncodeToString([]byte("x")),
	}, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := aws.ToString(mock.last.ContentType); got != "application/octet-stream" {
		t.Errorf("ContentType fallback = %q, want application/octet-stream", got)
	}
}

func TestS3UploadStep_BuildURL_Endpoint(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r", "key": "k", "body_from": "p",
		"endpoint": "https://minio.local/",
	})
	step := stepIface.(*s3UploadStep)
	got := step.buildURL("path/to/obj")
	want := "https://minio.local/b/path/to/obj"
	if got != want {
		t.Errorf("buildURL with endpoint = %q, want %q", got, want)
	}
}

func TestS3UploadStep_CredentialsRef(t *testing.T) {
	t.Cleanup(credref.Reset)
	want := awscreds.CredInput{AccessKey: "AKID", SecretKey: "SECRET", Source: "static"}
	_ = credref.Register("uploads-creds", want)

	p := NewS3UploadStepProvider()
	stepIface, err := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket":          "b",
		"region":          "us-west-2",
		"key":             "k",
		"body_from":       "p",
		"credentials_ref": "uploads-creds",
	})
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	step := stepIface.(*s3UploadStep)
	if step.cred.AccessKey != "AKID" || step.cred.SecretKey != "SECRET" {
		t.Errorf("resolved cred keys = %q/%q, want AKID/SECRET",
			step.cred.AccessKey, step.cred.SecretKey)
	}
	if step.cred.Region != "us-west-2" {
		t.Errorf("cred.Region = %q, want us-west-2 (step region overrides ref's)", step.cred.Region)
	}
}

func TestS3UploadStep_CredentialsRef_MissingErrors(t *testing.T) {
	t.Cleanup(credref.Reset)
	p := NewS3UploadStepProvider()
	_, err := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r", "key": "k", "body_from": "p",
		"credentials_ref": "does-not-exist",
	})
	if err == nil || !strings.Contains(err.Error(), "credentials_ref \"does-not-exist\" not found") {
		t.Errorf("err = %v, want missing-ref error", err)
	}
}

func TestS3UploadStep_KeyTemplate_UUIDFunc(t *testing.T) {
	p := NewS3UploadStepProvider()
	stepIface, _ := p.CreateStep("step.s3_upload", "x", map[string]any{
		"bucket": "b", "region": "r",
		"key":       "{{uuid}}.bin",
		"body_from": "payload",
	})
	step := stepIface.(*s3UploadStep)
	mock := newMockPutObject()
	step.SetTestClient(mock)
	res, err := step.Execute(context.Background(), nil, nil, map[string]any{
		"payload": base64.StdEncoding.EncodeToString([]byte("data")),
	}, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resolvedKey, _ := res.Output["key"].(string)
	if !strings.HasSuffix(resolvedKey, ".bin") || len(resolvedKey) < len("00000000-0000-0000-0000-000000000000.bin") {
		t.Errorf("uuid template did not resolve: key=%q", resolvedKey)
	}
}

// mockPutObject implements s3PutObjectAPI for tests; captures the last input.
type mockPutObject struct {
	mu   sync.Mutex
	last *s3.PutObjectInput
	err  error
}

func newMockPutObject() *mockPutObject { return &mockPutObject{} }

func (m *mockPutObject) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	// Drain Body into a buffer so test assertions can re-read it after Execute returns.
	if in.Body != nil {
		buf, _ := io.ReadAll(in.Body)
		in.Body = io.NopCloser(bytes.NewReader(buf))
	}
	m.last = in
	return &s3.PutObjectOutput{}, nil
}

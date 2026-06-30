package steps

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/modules"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func TestS3DownloadStepProvider_StepTypes(t *testing.T) {
	p := NewS3DownloadStepProvider()
	if got := p.StepTypes(); len(got) != 1 || got[0] != "step.s3_download" {
		t.Errorf("StepTypes = %v, want [step.s3_download]", got)
	}
}

func TestS3DownloadStepProvider_CreateStep_RequiredFields(t *testing.T) {
	p := NewS3DownloadStepProvider()
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"missing bucket", map[string]any{"region": "r", "key": "k"}, "'bucket' is required"},
		{"missing region", map[string]any{"bucket": "b", "key": "k"}, "'region' is required"},
		{"missing key", map[string]any{"bucket": "b", "region": "r"}, "'key' is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.CreateStep("step.s3_download", "x", tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestS3DownloadStep_Execute_UsesStorageS3GetObject(t *testing.T) {
	p := NewS3DownloadStepProvider()
	stepIface, err := p.CreateStep("step.s3_download", "download", map[string]any{
		"bucket":       "media-bucket",
		"region":       "us-east-1",
		"key":          "inputs/{{.job_id}}.mov",
		"output_name":  "payload",
		"artifact":     "source_media",
		"content_type": "video/quicktime",
		"credentials": map[string]any{
			"type":      "static",
			"accessKey": "AKIA_TEST",
			"secretKey": "SHOULD_NOT_LEAK",
		},
	})
	if err != nil {
		t.Fatalf("CreateStep: %v", err)
	}
	step := stepIface.(*s3DownloadStep)

	fakeS3 := newMockDownloadS3()
	fakeS3.objects["inputs/job-7.mov"] = []byte("MOVDATA")
	step.SetStorageFactory(func(name string, config map[string]any) (s3ObjectStorage, error) {
		inst, err := modules.NewS3StorageProvider().CreateModule("storage.s3", name, config)
		if err != nil {
			return nil, err
		}
		injectable, ok := inst.(interface{ SetTestClient(modules.S3API) })
		if !ok {
			return nil, errors.New("storage.s3 instance does not expose test client injection")
		}
		injectable.SetTestClient(fakeS3)
		storage, ok := inst.(s3ObjectStorage)
		if !ok {
			return nil, errors.New("storage.s3 instance does not expose GetObject")
		}
		return storage, nil
	})

	res, err := step.Execute(
		context.Background(),
		nil,
		nil,
		map[string]any{"job_id": "job-7"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fakeS3.getCalls != 1 {
		t.Fatalf("GetObject calls = %d, want 1", fakeS3.getCalls)
	}
	if fakeS3.bucket != "media-bucket" || fakeS3.key != "inputs/job-7.mov" {
		t.Fatalf("GetObject bucket/key = %q/%q, want media-bucket/inputs/job-7.mov", fakeS3.bucket, fakeS3.key)
	}
	wantBody := base64.StdEncoding.EncodeToString([]byte("MOVDATA"))
	if got := res.Output["payload"]; got != wantBody {
		t.Errorf("Output[payload] = %v, want %q", got, wantBody)
	}
	if got := res.Output["artifact"]; got != "source_media" {
		t.Errorf("Output[artifact] = %v, want source_media", got)
	}
	if got := res.Output["content_type"]; got != "video/quicktime" {
		t.Errorf("Output[content_type] = %v, want video/quicktime", got)
	}
	if got := res.Output["content_ref"]; got != "s3://media-bucket/inputs/job-7.mov" {
		t.Errorf("Output[content_ref] = %v, want s3://media-bucket/inputs/job-7.mov", got)
	}
	if leaked := outputContains(res.Output, "SHOULD_NOT_LEAK") || outputContains(res.Output, "AKIA_TEST"); leaked {
		t.Fatalf("step output leaked credential material: %#v", res.Output)
	}
}

func TestS3DownloadStep_DoesNotImportAWSS3SDK(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source := filepath.Join(filepath.Dir(file), "s3_download.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	for _, imp := range parsed.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "github.com/aws/aws-sdk-go-v2/service/s3" {
			t.Fatalf("step.s3_download must reuse storage.s3 GetObject; found direct AWS S3 SDK import in %s", source)
		}
	}
}

func TestS3DownloadStepProvider_ImplementsStepProvider(t *testing.T) {
	var _ sdk.StepProvider = (*S3DownloadStepProvider)(nil)
}

type mockDownloadS3 struct {
	mu       sync.Mutex
	objects  map[string][]byte
	bucket   string
	key      string
	getCalls int
}

func newMockDownloadS3() *mockDownloadS3 {
	return &mockDownloadS3{objects: make(map[string][]byte)}
}

func (m *mockDownloadS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	m.objects[aws.ToString(in.Key)] = data
	return &s3.PutObjectOutput{}, nil
}

func (m *mockDownloadS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	m.bucket = aws.ToString(in.Bucket)
	m.key = aws.ToString(in.Key)
	data, ok := m.objects[m.key]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (m *mockDownloadS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func outputContains(output map[string]any, needle string) bool {
	for _, value := range output {
		if strings.Contains(fmt.Sprint(value), needle) {
			return true
		}
	}
	return false
}

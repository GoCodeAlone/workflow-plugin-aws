package steps

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/modules"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

type s3ObjectStorage interface {
	sdk.ModuleInstance
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

type s3ObjectStorageFactory func(name string, config map[string]any) (s3ObjectStorage, error)

type S3DownloadStepProvider struct{}

func NewS3DownloadStepProvider() *S3DownloadStepProvider {
	return &S3DownloadStepProvider{}
}

func (p *S3DownloadStepProvider) StepTypes() []string {
	return []string{"step.s3_download"}
}

func (p *S3DownloadStepProvider) CreateStep(_, name string, config map[string]any) (sdk.StepInstance, error) {
	bucket := os.ExpandEnv(stringField(config, "bucket"))
	storageRef := stringField(config, "storage_ref")
	if bucket == "" {
		return nil, fmt.Errorf("step.s3_download %q: 'bucket' is required", name)
	}
	region := os.ExpandEnv(stringField(config, "region"))
	if region == "" {
		return nil, fmt.Errorf("step.s3_download %q: 'region' is required", name)
	}
	key := stringField(config, "key")
	if key == "" {
		return nil, fmt.Errorf("step.s3_download %q: 'key' is required", name)
	}
	outputName := stringField(config, "output_name")
	if outputName == "" {
		outputName = "body"
	}

	storageConfig := copyStorageConfig(config)
	storageConfig["bucket"] = bucket
	storageConfig["region"] = region
	storageConfig["endpoint"] = os.ExpandEnv(stringField(config, "endpoint"))

	return &s3DownloadStep{
		name:          name,
		bucket:        bucket,
		region:        region,
		storageRef:    storageRef,
		keyTmpl:       key,
		outputName:    outputName,
		artifact:      stringField(config, "artifact"),
		contentType:   stringField(config, "content_type"),
		storageConfig: storageConfig,
		storageFactory: func(storageName string, cfg map[string]any) (s3ObjectStorage, error) {
			return defaultS3ObjectStorageFactory(storageName, cfg)
		},
	}, nil
}

type s3DownloadStep struct {
	name          string
	bucket        string
	region        string
	storageRef    string
	keyTmpl       string
	outputName    string
	artifact      string
	contentType   string
	storageConfig map[string]any

	storageFactory s3ObjectStorageFactory
}

func (s *s3DownloadStep) SetStorageFactory(factory s3ObjectStorageFactory) {
	s.storageFactory = factory
}

func (s *s3DownloadStep) Execute(
	ctx context.Context,
	_ map[string]any,
	stepOutputs map[string]map[string]any,
	current map[string]any,
	_ map[string]any,
	_ map[string]any,
) (*sdk.StepResult, error) {
	pcData := buildPipelineData(stepOutputs, current)
	resolvedKey, err := renderTemplate(s.keyTmpl, pcData)
	if err != nil {
		return nil, fmt.Errorf("step.s3_download %q: resolve key template: %w", s.name, err)
	}

	storage, err := s.storageFactory(s.storageName(), cloneMap(s.storageConfig))
	if err != nil {
		return nil, fmt.Errorf("step.s3_download %q: create storage.s3: %w", s.name, err)
	}
	if err := storage.Init(); err != nil {
		return nil, fmt.Errorf("step.s3_download %q: init storage.s3: %w", s.name, err)
	}
	if err := storage.Start(ctx); err != nil {
		return nil, fmt.Errorf("step.s3_download %q: start storage.s3: %w", s.name, err)
	}
	defer func() { _ = storage.Stop(ctx) }()

	body, err := storage.GetObject(ctx, resolvedKey)
	if err != nil {
		return nil, fmt.Errorf("step.s3_download %q: GetObject %q: %w", s.name, resolvedKey, err)
	}
	defer func() { _ = body.Close() }()

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("step.s3_download %q: read %q: %w", s.name, resolvedKey, err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	out := map[string]any{
		"body":         encoded,
		"bucket":       s.bucket,
		"key":          resolvedKey,
		"content_ref":  s.contentRef(resolvedKey),
		"storage_ref":  s.storageRef,
		"content_size": len(data),
	}
	if s.outputName != "" && s.outputName != "body" {
		out[s.outputName] = encoded
	}
	if s.artifact != "" {
		out["artifact"] = s.artifact
	}
	if s.contentType != "" {
		out["content_type"] = s.contentType
	}
	return &sdk.StepResult{Output: out}, nil
}

func (s *s3DownloadStep) storageName() string {
	if s.storageRef != "" {
		return s.storageRef
	}
	return s.name + "-storage"
}

func (s *s3DownloadStep) contentRef(key string) string {
	if s.bucket != "" {
		return "s3://" + s.bucket + "/" + strings.TrimLeft(key, "/")
	}
	return "content://" + strings.TrimLeft(s.storageRef+"/"+key, "/")
}

func defaultS3ObjectStorageFactory(name string, config map[string]any) (s3ObjectStorage, error) {
	inst, err := modules.NewS3StorageProvider().CreateModule("storage.s3", name, config)
	if err != nil {
		return nil, err
	}
	storage, ok := inst.(s3ObjectStorage)
	if !ok {
		return nil, fmt.Errorf("storage.s3 %q does not expose GetObject", name)
	}
	return storage, nil
}

func copyStorageConfig(config map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"bucket", "region", "endpoint", "credentials_ref", "credentials"} {
		if value, ok := config[key]; ok {
			out[key] = value
		}
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

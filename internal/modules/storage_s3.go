// storage_s3.go — plugin-native storage.s3 module.
//
// Ports workflow core's module/s3_storage.go (S3Storage) into the aws
// plugin. Credentials flow through awscreds.BuildAWSConfig: either an
// inline `credentials:` block in the module config, or `credentials_ref:`
// resolving to an aws.credentials module registered in the credref registry.
package modules

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/credref"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// S3StorageProvider implements sdk.ModuleProvider for the "storage.s3"
// standalone-module type.
type S3StorageProvider struct{}

// NewS3StorageProvider returns a fresh provider.
func NewS3StorageProvider() *S3StorageProvider {
	return &S3StorageProvider{}
}

// ModuleTypes reports the single module type this Provider serves.
func (p *S3StorageProvider) ModuleTypes() []string {
	return []string{"storage.s3"}
}

// CreateModule parses the storage.s3 config and returns a lifecycle-ready
// module instance. Bucket is required. Credentials come from either an
// inline `credentials:` sub-block OR `credentials_ref:` (a sibling
// aws.credentials module name registered in the credref registry).
// `credentials:` and `credentials_ref:` are mutually exclusive; inline wins
// when both are supplied (mirroring upstream config-merge semantics).
func (p *S3StorageProvider) CreateModule(_, name string, config map[string]any) (sdk.ModuleInstance, error) {
	bucket := stringField(config, "bucket")
	if bucket == "" {
		return nil, fmt.Errorf("storage.s3 %q: 'bucket' is required", name)
	}

	cred, err := resolveAWSCredentials(name, config)
	if err != nil {
		return nil, err
	}

	return &s3StorageInstance{
		name:     name,
		bucket:   bucket,
		region:   stringField(config, "region"),
		endpoint: stringField(config, "endpoint"),
		cred:     cred,
	}, nil
}

// resolveAWSCredentials decodes the config's credentials surface into an
// awscreds.CredInput. An inline `credentials:` block beats `credentials_ref:`;
// a credentials_ref to an unregistered name is a clean error.
func resolveAWSCredentials(moduleName string, config map[string]any) (awscreds.CredInput, error) {
	region := stringField(config, "region")
	if credsMap, ok := config["credentials"].(map[string]any); ok && len(credsMap) > 0 {
		return awscreds.CredInput{
			Region:       region,
			AccessKey:    stringField(credsMap, "accessKey"),
			SecretKey:    stringField(credsMap, "secretKey"),
			SessionToken: stringField(credsMap, "sessionToken"),
			RoleARN:      stringField(credsMap, "roleArn"),
			ExternalID:   stringField(credsMap, "externalId"),
			Profile:      stringField(credsMap, "profile"),
			SessionName:  stringField(credsMap, "sessionName"),
			Source:       stringField(credsMap, "type"),
		}, nil
	}
	if ref := stringField(config, "credentials_ref"); ref != "" {
		c, ok := credref.Resolve(ref)
		if !ok {
			return awscreds.CredInput{}, fmt.Errorf(
				"storage.s3 %q: credentials_ref %q not found; declare an aws.credentials module first",
				moduleName, ref)
		}
		// If the module's own region overrides the referenced creds' region,
		// honour it — the storage module's region wins over the cred's.
		if region != "" {
			c.Region = region
		}
		return c, nil
	}
	// No credentials surface → default credential chain (BuildAWSConfig
	// returns a default-chain config; honour region if set).
	return awscreds.CredInput{Region: region}, nil
}

// s3API is the subset of *s3.Client the storage module calls. Lets tests
// inject a mock without spinning up a real S3 endpoint.
type s3API interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// s3StorageInstance is the lifecycle + storage surface returned by
// CreateModule.
type s3StorageInstance struct {
	name     string
	bucket   string
	region   string
	endpoint string
	cred     awscreds.CredInput

	mu     sync.Mutex
	client s3API
}

// SetTestClient injects a fake s3API for tests so Storage operations can be
// exercised without a real S3 endpoint.
func (m *s3StorageInstance) SetTestClient(c s3API) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = c
}

func (m *s3StorageInstance) Init() error { return nil }

func (m *s3StorageInstance) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		// SetTestClient already populated; skip real client construction.
		return nil
	}
	cfg, err := awscreds.BuildAWSConfig(ctx, m.cred)
	if err != nil {
		return fmt.Errorf("storage.s3 %q: load AWS config: %w", m.name, err)
	}
	if m.region != "" {
		cfg.Region = m.region
	}

	var opts []func(*s3.Options)
	if m.endpoint != "" {
		ep := m.endpoint
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = &ep
			o.UsePathStyle = true
		})
	}
	m.client = s3.NewFromConfig(cfg, opts...)
	return nil
}

func (m *s3StorageInstance) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = nil
	return nil
}

func (m *s3StorageInstance) getClient() (s3API, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return nil, fmt.Errorf("storage.s3 %q: client not initialized; call Start first", m.name)
	}
	return m.client, nil
}

// PutObject uploads an object to S3.
func (m *s3StorageInstance) PutObject(ctx context.Context, key string, body io.Reader) error {
	c, err := m.getClient()
	if err != nil {
		return err
	}
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &m.bucket,
		Key:    &key,
		Body:   body,
	}); err != nil {
		return fmt.Errorf("storage.s3 %q: put %q: %w", m.name, key, err)
	}
	return nil
}

// GetObject retrieves an object from S3.
func (m *s3StorageInstance) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	c, err := m.getClient()
	if err != nil {
		return nil, err
	}
	result, err := c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &m.bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("storage.s3 %q: get %q: %w", m.name, key, err)
	}
	return result.Body, nil
}

// DeleteObject removes an object from S3.
func (m *s3StorageInstance) DeleteObject(ctx context.Context, key string) error {
	c, err := m.getClient()
	if err != nil {
		return err
	}
	if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &m.bucket,
		Key:    &key,
	}); err != nil {
		return fmt.Errorf("storage.s3 %q: delete %q: %w", m.name, key, err)
	}
	return nil
}

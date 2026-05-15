// Package statebackend provides the s3 IaCStateBackend implementation, ported
// from workflow core's module/iac_state_spaces.go (cloud-SDK-extraction effort,
// decisions/0033-0036). It is self-contained: it carries its own IaCState type,
// IaCStateStore interface, and S3Client interface so the plugin can SERVE the
// s3 backend over the typed IaCStateBackend gRPC contract without depending on
// workflow core's unexported state helpers.
//
// Unlike workflow-plugin-digitalocean's port (which serves `spaces` and keeps
// the DO_SPACES_* env fallbacks), this aws-plugin port deliberately drops the
// DigitalOcean-specific credential env fallbacks: when no static credentials
// are supplied it defers to aws-sdk-go-v2's default credential chain.
package statebackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// IaCState tracks the state of an infrastructure resource. Mirrors
// module.IaCState (workflow core module/iac_state.go) and the proto IaCState
// message (plugin/external/proto/iac.proto) — kept local so this package is
// self-contained.
type IaCState struct {
	ResourceID   string         `json:"resource_id"`
	ResourceType string         `json:"resource_type"` // e.g. "kubernetes", "ecs"
	Provider     string         `json:"provider"`      // e.g. "aws", "gcp", "local"
	ProviderRef  string         `json:"provider_ref,omitempty"`
	ProviderID   string         `json:"provider_id,omitempty"`
	ConfigHash   string         `json:"config_hash,omitempty"`
	Status       string         `json:"status"`  // planned, provisioning, active, destroying, destroyed, error
	Outputs      map[string]any `json:"outputs"` // provider-specific outputs
	Config       map[string]any `json:"config"`  // the config used to provision
	Dependencies []string       `json:"dependencies,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	Error        string         `json:"error,omitempty"`
}

// IaCStateStore is the interface for IaC state persistence backends. Mirrors
// the ctx-ful module.IaCStateStore (workflow core module/iac_state.go) — kept
// local so this package is self-contained.
type IaCStateStore interface {
	// GetState retrieves a state record by resource ID. Returns nil, nil when not found.
	GetState(ctx context.Context, resourceID string) (*IaCState, error)
	// SaveState inserts or replaces a state record.
	SaveState(ctx context.Context, state *IaCState) error
	// ListStates returns all state records matching the provided key=value filter.
	// Pass a nil or empty map to return all records.
	ListStates(ctx context.Context, filter map[string]string) ([]*IaCState, error)
	// DeleteState removes a state record by resource ID.
	DeleteState(ctx context.Context, resourceID string) error
	// Lock acquires an exclusive lock for the given resource ID.
	Lock(ctx context.Context, resourceID string) error
	// Unlock releases the lock for the given resource ID.
	Unlock(ctx context.Context, resourceID string) error
}

// sanitizeID replaces path-unsafe characters so resource IDs can be used as object keys.
func sanitizeID(id string) string {
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	return id
}

// matchesFilter returns true if state satisfies all entries in the filter map.
// Only the allow-listed keys "resource_type", "provider", and "status" are
// honored — any other key is ignored. This mirrors the FS and in-memory
// IaCStateStore implementations in workflow core so all backends filter
// identically.
func matchesFilter(st *IaCState, filter map[string]string) bool {
	for k, v := range filter {
		switch k {
		case "resource_type":
			if st.ResourceType != v {
				return false
			}
		case "provider":
			if st.Provider != v {
				return false
			}
		case "status":
			if st.Status != v {
				return false
			}
		}
	}
	return true
}

// S3Client abstracts the S3 API methods used by S3IaCStateStore, allowing a
// mock to be injected for testing.
type S3Client interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	HeadObject(ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// S3IaCStateStore persists IaC state as JSON objects in an S3 (or S3-compatible)
// bucket. Lock objects are used for advisory locking.
type S3IaCStateStore struct {
	client S3Client
	bucket string
	prefix string
	mu     sync.Mutex
}

// NewS3IaCStateStore creates an S3 / S3-compatible state store.
//
// Parameters:
//   - region: AWS region (e.g. "us-east-1"); used by the SDK to resolve the
//     S3 endpoint unless endpoint is set.
//   - bucket: S3 bucket name (required).
//   - prefix: optional key prefix (default "iac-state/").
//   - accessKey: optional static access key. When accessKey/secretKey are both
//     empty, the aws-sdk-go-v2 default credential chain
//     (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY env, shared config, instance
//     role, etc.) applies — no DigitalOcean-specific env fallback.
//   - secretKey: optional static secret key (see accessKey).
//   - endpoint: optional custom endpoint override for S3-compatible stores.
func NewS3IaCStateStore(region, bucket, prefix, accessKey, secretKey, endpoint string) (*S3IaCStateStore, error) {
	if bucket == "" {
		return nil, fmt.Errorf("iac s3 state: bucket must not be empty")
	}
	if prefix == "" {
		prefix = "iac-state/"
	}
	if region == "" && endpoint == "" {
		return nil, fmt.Errorf("iac s3 state: either region or endpoint must be set")
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(regionOrDefault(region)),
	}
	// Only inject static credentials when BOTH are explicitly provided.
	// Otherwise defer to aws-sdk-go-v2's default credential chain — there is
	// deliberately no DigitalOcean-specific env-var fallback here.
	if accessKey != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("iac s3 state: load config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
		}
		o.UsePathStyle = true
	})

	return &S3IaCStateStore{
		client: client,
		bucket: bucket,
		prefix: prefix,
	}, nil
}

// NewS3IaCStateStoreWithClient creates a store with an injected client (for testing).
func NewS3IaCStateStoreWithClient(client S3Client, bucket, prefix string) *S3IaCStateStore {
	if prefix == "" {
		prefix = "iac-state/"
	}
	return &S3IaCStateStore{
		client: client,
		bucket: bucket,
		prefix: prefix,
	}
}

func regionOrDefault(region string) string {
	if region == "" {
		return "us-east-1"
	}
	return region
}

// stateKey returns the S3 key for a resource's state JSON.
func (s *S3IaCStateStore) stateKey(resourceID string) string {
	return s.prefix + sanitizeID(resourceID) + ".json"
}

// lockKey returns the S3 key for a resource's lock object.
func (s *S3IaCStateStore) lockKey(resourceID string) string {
	return s.prefix + sanitizeID(resourceID) + ".lock"
}

// GetState retrieves a state record by resource ID. Returns nil, nil when not found.
func (s *S3IaCStateStore) GetState(ctx context.Context, resourceID string) (*IaCState, error) {
	key := s.stateKey(resourceID)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		if isNotFoundErr(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("iac s3 state: GetState %q: %w", resourceID, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("iac s3 state: GetState %q: read body: %w", resourceID, err)
	}

	var st IaCState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("iac s3 state: GetState %q: unmarshal: %w", resourceID, err)
	}
	return &st, nil
}

// SaveState writes the state record as a JSON object to S3.
func (s *S3IaCStateStore) SaveState(ctx context.Context, state *IaCState) error {
	if state == nil {
		return fmt.Errorf("iac s3 state: SaveState: state must not be nil")
	}
	if state.ResourceID == "" {
		return fmt.Errorf("iac s3 state: SaveState: resource_id must not be empty")
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("iac s3 state: SaveState %q: marshal: %w", state.ResourceID, err)
	}

	key := s.stateKey(state.ResourceID)
	contentType := "application/json"
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
	})
	if err != nil {
		return fmt.Errorf("iac s3 state: SaveState %q: put: %w", state.ResourceID, err)
	}
	return nil
}

// ListStates lists all state objects under the prefix and returns those matching filter.
// Supported filter keys: "resource_type", "provider", "status".
func (s *S3IaCStateStore) ListStates(ctx context.Context, filter map[string]string) ([]*IaCState, error) {
	var results []*IaCState
	var continuationToken *string

	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &s.bucket,
			Prefix:            &s.prefix,
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("iac s3 state: ListStates: %w", err)
		}

		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			// Skip lock files and non-JSON objects.
			if strings.HasSuffix(key, ".lock") || !strings.HasSuffix(key, ".json") {
				continue
			}

			getOut, err := s.client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: &s.bucket,
				Key:    obj.Key,
			})
			if err != nil {
				continue // skip unreadable objects
			}
			data, err := io.ReadAll(getOut.Body)
			getOut.Body.Close()
			if err != nil {
				continue
			}

			var st IaCState
			if err := json.Unmarshal(data, &st); err != nil {
				continue
			}
			if matchesFilter(&st, filter) {
				results = append(results, &st)
			}
		}

		if !aws.ToBool(out.IsTruncated) {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	return results, nil
}

// DeleteState removes the state object for resourceID.
func (s *S3IaCStateStore) DeleteState(ctx context.Context, resourceID string) error {
	// Verify existence first to return a meaningful error.
	key := s.stateKey(resourceID)
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		if isNotFoundErr(err) {
			return fmt.Errorf("iac s3 state: DeleteState %q: not found", resourceID)
		}
		return fmt.Errorf("iac s3 state: DeleteState %q: head: %w", resourceID, err)
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("iac s3 state: DeleteState %q: %w", resourceID, err)
	}
	return nil
}

// Lock creates a lock object for resourceID using S3 conditional writes (If-None-Match: *)
// for atomic, race-free lock acquisition. Fails if the lock already exists.
func (s *S3IaCStateStore) Lock(ctx context.Context, resourceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.lockKey(resourceID)
	body := []byte(time.Now().UTC().Format(time.RFC3339))
	ifNoneMatch := "*"

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(body),
		IfNoneMatch: &ifNoneMatch,
	})
	if err != nil {
		// S3 returns 412 Precondition Failed when the object already exists.
		if isPreconditionFailedErr(err) {
			return fmt.Errorf("iac s3 state: Lock %q: resource is already locked", resourceID)
		}
		return fmt.Errorf("iac s3 state: Lock %q: put: %w", resourceID, err)
	}
	return nil
}

// Unlock removes the lock object for resourceID.
func (s *S3IaCStateStore) Unlock(ctx context.Context, resourceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.lockKey(resourceID)

	// Verify lock exists.
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		if isNotFoundErr(err) {
			return fmt.Errorf("iac s3 state: Unlock %q: not locked", resourceID)
		}
		return fmt.Errorf("iac s3 state: Unlock %q: head: %w", resourceID, err)
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("iac s3 state: Unlock %q: %w", resourceID, err)
	}
	return nil
}

// isPreconditionFailedErr returns true for HTTP 412 Precondition Failed responses,
// which S3 returns when a conditional write fails (e.g. If-None-Match: * on an existing object).
func isPreconditionFailedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "PreconditionFailed") || strings.Contains(msg, "412")
}

// isNotFoundErr checks whether an S3 error indicates the key was not found.
func isNotFoundErr(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	// HeadObject returns a generic "NotFound" status, not NoSuchKey.
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	// Some S3-compatible stores return a plain "not found" in the message.
	return strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "NoSuchKey")
}

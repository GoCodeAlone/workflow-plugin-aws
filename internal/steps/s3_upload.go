// Package steps implements the aws plugin's standalone pipeline steps.
// step.s3_upload uploads base64-encoded body content from the pipeline
// context to S3 and returns {url, key, bucket} in the step output.
//
// Ports workflow core's module/pipeline_step_s3_upload.go behavior into the
// plugin. Credentials flow through awscreds.BuildAWSConfig: either an
// inline `credentials:` block in the step config, or `credentials_ref:`
// resolving to an aws.credentials module registered in the credref registry.
package steps

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
	"github.com/GoCodeAlone/workflow-plugin-aws/internal/credref"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// s3PutObjectAPI is the minimal S3 surface the step calls. Lets tests inject
// a fake without spinning up a real S3 endpoint.
type s3PutObjectAPI interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3UploadStepProvider implements sdk.StepProvider for "step.s3_upload".
type S3UploadStepProvider struct{}

// NewS3UploadStepProvider returns a fresh provider.
func NewS3UploadStepProvider() *S3UploadStepProvider {
	return &S3UploadStepProvider{}
}

// StepTypes reports the single step type this Provider serves.
func (p *S3UploadStepProvider) StepTypes() []string {
	return []string{"step.s3_upload"}
}

// CreateStep parses the step config and returns a ready-to-Execute instance.
// Required: bucket, region, key, body_from. Optional: endpoint,
// content_type, content_type_from, credentials | credentials_ref.
func (p *S3UploadStepProvider) CreateStep(_, name string, config map[string]any) (sdk.StepInstance, error) {
	bucket := os.ExpandEnv(stringField(config, "bucket"))
	if bucket == "" {
		return nil, fmt.Errorf("step.s3_upload %q: 'bucket' is required", name)
	}
	region := os.ExpandEnv(stringField(config, "region"))
	if region == "" {
		return nil, fmt.Errorf("step.s3_upload %q: 'region' is required", name)
	}
	key := stringField(config, "key")
	if key == "" {
		return nil, fmt.Errorf("step.s3_upload %q: 'key' is required", name)
	}
	bodyFrom := stringField(config, "body_from")
	if bodyFrom == "" {
		return nil, fmt.Errorf("step.s3_upload %q: 'body_from' is required", name)
	}

	cred, err := resolveCreds(name, config, region)
	if err != nil {
		return nil, err
	}

	return &s3UploadStep{
		name:            name,
		bucket:          bucket,
		region:          region,
		keyTmpl:         key,
		bodyFrom:        bodyFrom,
		contentType:     stringField(config, "content_type"),
		contentTypeFrom: stringField(config, "content_type_from"),
		endpoint:        os.ExpandEnv(stringField(config, "endpoint")),
		cred:            cred,
	}, nil
}

// resolveCreds reads the inline `credentials:` block or `credentials_ref:`
// and returns the awscreds.CredInput. Inline beats ref; missing ref errors.
func resolveCreds(stepName string, config map[string]any, region string) (awscreds.CredInput, error) {
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
				"step.s3_upload %q: credentials_ref %q not found; declare an aws.credentials module first",
				stepName, ref)
		}
		if region != "" {
			c.Region = region
		}
		return c, nil
	}
	return awscreds.CredInput{Region: region}, nil
}

// s3UploadStep is the StepInstance returned by CreateStep.
type s3UploadStep struct {
	name            string
	bucket          string
	region          string
	keyTmpl         string
	bodyFrom        string
	contentType     string
	contentTypeFrom string
	endpoint        string
	cred            awscreds.CredInput

	// testClient is an optional injection seam — tests set it to bypass
	// real S3 client construction.
	testClient s3PutObjectAPI
}

// SetTestClient injects a fake S3 client for tests.
func (s *s3UploadStep) SetTestClient(c s3PutObjectAPI) { s.testClient = c }

// Execute uploads the resolved body to S3 and returns {url, key, bucket}.
func (s *s3UploadStep) Execute(
	ctx context.Context,
	_ map[string]any, // triggerData (unused — body comes from step outputs / current)
	stepOutputs map[string]map[string]any,
	current map[string]any,
	_ map[string]any, // metadata (unused)
	_ map[string]any, // config (parsed at CreateStep)
) (*sdk.StepResult, error) {
	pcData := buildPipelineData(stepOutputs, current)

	resolvedKey, err := renderTemplate(s.keyTmpl, pcData)
	if err != nil {
		return nil, fmt.Errorf("step.s3_upload %q: resolve key template: %w", s.name, err)
	}

	bodyVal, err := resolveDottedPath(pcData, s.bodyFrom)
	if err != nil {
		return nil, fmt.Errorf("step.s3_upload %q: body_from %q: %w", s.name, s.bodyFrom, err)
	}
	bodyStr, ok := bodyVal.(string)
	if !ok {
		return nil, fmt.Errorf("step.s3_upload %q: body_from value must be a base64 string, got %T", s.name, bodyVal)
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("step.s3_upload %q: base64 decode body: %w", s.name, err)
	}

	contentType := s.resolveContentType(pcData)

	client, err := s.getClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("step.s3_upload %q: build S3 client: %w", s.name, err)
	}

	input := &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &resolvedKey,
		Body:   bytes.NewReader(bodyBytes),
	}
	if contentType != "" {
		input.ContentType = &contentType
	}
	if _, err := client.PutObject(ctx, input); err != nil {
		return nil, fmt.Errorf("step.s3_upload %q: PutObject %q: %w", s.name, resolvedKey, err)
	}

	return &sdk.StepResult{
		Output: map[string]any{
			"url":    s.buildURL(resolvedKey),
			"key":    resolvedKey,
			"bucket": s.bucket,
		},
	}, nil
}

func (s *s3UploadStep) resolveContentType(pcData map[string]any) string {
	if s.contentTypeFrom != "" {
		if v, err := resolveDottedPath(pcData, s.contentTypeFrom); err == nil {
			if ct, ok := v.(string); ok && ct != "" {
				return ct
			}
		}
	}
	return s.contentType
}

func (s *s3UploadStep) getClient(ctx context.Context) (s3PutObjectAPI, error) {
	if s.testClient != nil {
		return s.testClient, nil
	}
	cfg, err := awscreds.BuildAWSConfig(ctx, s.cred)
	if err != nil {
		return nil, err
	}
	if s.region != "" {
		cfg.Region = s.region
	}
	var opts []func(*s3.Options)
	if s.endpoint != "" {
		ep := s.endpoint
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = &ep
			o.UsePathStyle = true
		})
	}
	return s3.NewFromConfig(cfg, opts...), nil
}

// buildURL returns the virtual-hosted-style URL for the uploaded object, or
// an endpoint-prefixed URL when a custom endpoint is configured.
func (s *s3UploadStep) buildURL(key string) string {
	if s.endpoint != "" {
		return strings.TrimRight(s.endpoint, "/") + "/" + s.bucket + "/" + key
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, key)
}

// ── Helpers (mirror workflow core's pipeline_template + dot-path helpers) ───

// buildPipelineData merges Current map with step outputs under "steps".
func buildPipelineData(stepOutputs map[string]map[string]any, current map[string]any) map[string]any {
	data := make(map[string]any, len(current)+1)
	for k, v := range current {
		data[k] = v
	}
	if len(stepOutputs) > 0 {
		steps := make(map[string]any, len(stepOutputs))
		for k, v := range stepOutputs {
			steps[k] = v
		}
		data["steps"] = steps
	}
	return data
}

// resolveDottedPath walks a dot-separated path through nested map[string]any.
// "a.b.c" with data {a:{b:{c:42}}} returns 42.
func resolveDottedPath(data map[string]any, path string) (any, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	parts := strings.Split(path, ".")
	var cur any = data
	for i, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q: segment %d (%q) traversed a non-map", path, i, p)
		}
		v, ok := m[p]
		if !ok {
			return nil, fmt.Errorf("path %q: segment %d (%q) not found", path, i, p)
		}
		cur = v
	}
	return cur, nil
}

// renderTemplate renders a Go text/template with a uuid funcMap that mirrors
// upstream's TemplateEngine surface ({{ .field }} and {{ uuid }}).
func renderTemplate(tmpl string, data map[string]any) (string, error) {
	t, err := template.New("s3_upload_key").Funcs(template.FuncMap{
		"uuid": func() string { return uuid.New().String() },
	}).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute: %w", err)
	}
	return buf.String(), nil
}

func stringField(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

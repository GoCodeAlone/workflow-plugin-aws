package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// ACMClient is the subset of ACM API used by ACMDriver.
type ACMClient interface {
	RequestCertificate(ctx context.Context, params *acm.RequestCertificateInput, optFns ...func(*acm.Options)) (*acm.RequestCertificateOutput, error)
	DescribeCertificate(ctx context.Context, params *acm.DescribeCertificateInput, optFns ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
	ListCertificates(ctx context.Context, params *acm.ListCertificatesInput, optFns ...func(*acm.Options)) (*acm.ListCertificatesOutput, error)
	DeleteCertificate(ctx context.Context, params *acm.DeleteCertificateInput, optFns ...func(*acm.Options)) (*acm.DeleteCertificateOutput, error)
}

// ACMDriver manages ACM certificates (infra.certificate).
type ACMDriver struct {
	client ACMClient
}

// NewACMDriver creates an ACM driver from an AWS config.
func NewACMDriver(cfg awssdk.Config) *ACMDriver {
	return &ACMDriver{client: acm.NewFromConfig(cfg)}
}

// NewACMDriverWithClient creates an ACM driver with a custom client (for tests).
func NewACMDriverWithClient(client ACMClient) *ACMDriver {
	return &ACMDriver{client: client}
}

func (d *ACMDriver) ResourceType() string { return "infra.certificate" }

func (d *ACMDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	domain, _ := spec.Config["domain_name"].(string)
	if domain == "" {
		domain = spec.Name
	}
	validationMethod, _ := spec.Config["validation_method"].(string)
	if validationMethod == "" {
		validationMethod = "DNS"
	}
	altNames := stringSliceProp(spec.Config, "subject_alternative_names")

	in := &acm.RequestCertificateInput{
		DomainName:       awssdk.String(domain),
		ValidationMethod: acmtypes.ValidationMethod(validationMethod),
	}
	if len(altNames) > 0 {
		in.SubjectAlternativeNames = altNames
	}

	out, err := d.client.RequestCertificate(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("acm: request certificate %q: %w", spec.Name, err)
	}

	certARN := awssdk.ToString(out.CertificateArn)
	outputs := map[string]any{
		"arn":               certARN,
		"domain_name":       domain,
		"validation_method": validationMethod,
	}

	return &interfaces.ResourceOutput{
		Name:       spec.Name,
		Type:       "infra.certificate",
		ProviderID: certARN,
		Outputs:    outputs,
		Status:     "creating",
	}, nil
}

func (d *ACMDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if ref.ProviderID != "" {
		return d.describeCert(ctx, ref.Name, ref.ProviderID)
	}

	// Search by domain name
	out, err := d.client.ListCertificates(ctx, &acm.ListCertificatesInput{})
	if err != nil {
		return nil, fmt.Errorf("acm: list certificates: %w", err)
	}
	for _, cert := range out.CertificateSummaryList {
		if awssdk.ToString(cert.DomainName) == ref.Name {
			return d.describeCert(ctx, ref.Name, awssdk.ToString(cert.CertificateArn))
		}
	}
	return nil, fmt.Errorf("acm: certificate %q not found", ref.Name)
}

func (d *ACMDriver) describeCert(ctx context.Context, name, arn string) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: awssdk.String(arn),
	})
	if err != nil {
		return nil, fmt.Errorf("acm: describe certificate %q: %w", arn, err)
	}
	return acmCertToOutput(name, out.Certificate), nil
}

func (d *ACMDriver) Update(ctx context.Context, ref interfaces.ResourceRef, _ interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	// ACM certificates cannot be directly modified — return current state
	return d.Read(ctx, ref)
}

func (d *ACMDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	certARN := ref.ProviderID
	if certARN == "" {
		current, err := d.Read(ctx, ref)
		if err != nil {
			return err
		}
		certARN, _ = current.Outputs["arn"].(string)
	}
	if certARN == "" {
		return fmt.Errorf("acm: delete %q: no certificate ARN", ref.Name)
	}
	_, err := d.client.DeleteCertificate(ctx, &acm.DeleteCertificateInput{
		CertificateArn: awssdk.String(certARN),
	})
	if err != nil {
		return fmt.Errorf("acm: delete certificate %q: %w", ref.Name, err)
	}
	return nil
}

func (d *ACMDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *ACMDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	status, _ := out.Outputs["cert_status"].(string)
	healthy := status == "ISSUED"
	return &interfaces.HealthResult{Healthy: healthy, Message: status}, nil
}

func (d *ACMDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("acm: certificates are not scalable")
}

func acmCertToOutput(name string, cert *acmtypes.CertificateDetail) *interfaces.ResourceOutput {
	if cert == nil {
		return nil
	}
	certStatus := string(cert.Status)
	status := "creating"
	if cert.Status == acmtypes.CertificateStatusIssued {
		status = "running"
	} else if cert.Status == acmtypes.CertificateStatusFailed ||
		cert.Status == acmtypes.CertificateStatusRevoked {
		status = "failed"
	}

	outputs := map[string]any{"cert_status": certStatus}
	if cert.CertificateArn != nil {
		outputs["arn"] = *cert.CertificateArn
	}
	if cert.DomainName != nil {
		outputs["domain_name"] = *cert.DomainName
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.certificate",
		ProviderID: awssdk.ToString(cert.CertificateArn),
		Outputs:    outputs,
		Status:     status,
	}
}

// SensitiveKeys returns output keys whose values should be masked in logs and plan output.
func (d *ACMDriver) SensitiveKeys() []string { return nil }

var _ interfaces.ResourceDriver = (*ACMDriver)(nil)

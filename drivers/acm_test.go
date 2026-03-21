package drivers_test

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockACMClient struct {
	requestOut  *acm.RequestCertificateOutput
	requestErr  error
	describeOut *acm.DescribeCertificateOutput
	describeErr error
	listOut     *acm.ListCertificatesOutput
	listErr     error
	deleteErr   error
}

func (m *mockACMClient) RequestCertificate(_ context.Context, _ *acm.RequestCertificateInput, _ ...func(*acm.Options)) (*acm.RequestCertificateOutput, error) {
	return m.requestOut, m.requestErr
}
func (m *mockACMClient) DescribeCertificate(_ context.Context, _ *acm.DescribeCertificateInput, _ ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockACMClient) ListCertificates(_ context.Context, _ *acm.ListCertificatesInput, _ ...func(*acm.Options)) (*acm.ListCertificatesOutput, error) {
	return m.listOut, m.listErr
}
func (m *mockACMClient) DeleteCertificate(_ context.Context, _ *acm.DeleteCertificateInput, _ ...func(*acm.Options)) (*acm.DeleteCertificateOutput, error) {
	return &acm.DeleteCertificateOutput{}, m.deleteErr
}

func TestACMDriver_Create(t *testing.T) {
	certARN := "arn:aws:acm:us-east-1:123:certificate/abc-123"
	mock := &mockACMClient{
		requestOut: &acm.RequestCertificateOutput{
			CertificateArn: awssdk.String(certARN),
		},
	}
	d := drivers.NewACMDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "example.com",
		Config: map[string]any{"domain_name": "example.com", "validation_method": "DNS"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.certificate" {
		t.Errorf("expected infra.certificate, got %s", out.Type)
	}
	if out.ProviderID != certARN {
		t.Errorf("expected ProviderID %s, got %s", certARN, out.ProviderID)
	}
	if out.Status != "creating" {
		t.Errorf("expected status creating, got %s", out.Status)
	}
}

func TestACMDriver_Read_ByProviderID(t *testing.T) {
	certARN := "arn:aws:acm:us-east-1:123:certificate/abc-123"
	mock := &mockACMClient{
		describeOut: &acm.DescribeCertificateOutput{
			Certificate: &acmtypes.CertificateDetail{
				CertificateArn: awssdk.String(certARN),
				DomainName:     awssdk.String("example.com"),
				Status:         acmtypes.CertificateStatusIssued,
			},
		},
	}
	d := drivers.NewACMDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "example.com", ProviderID: certARN})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Status != "running" {
		t.Errorf("expected running, got %s", out.Status)
	}
}

func TestACMDriver_Read_ByDomainName(t *testing.T) {
	certARN := "arn:aws:acm:us-east-1:123:certificate/abc-123"
	mock := &mockACMClient{
		listOut: &acm.ListCertificatesOutput{
			CertificateSummaryList: []acmtypes.CertificateSummary{
				{CertificateArn: awssdk.String(certARN), DomainName: awssdk.String("example.com")},
			},
		},
		describeOut: &acm.DescribeCertificateOutput{
			Certificate: &acmtypes.CertificateDetail{
				CertificateArn: awssdk.String(certARN),
				DomainName:     awssdk.String("example.com"),
				Status:         acmtypes.CertificateStatusPendingValidation,
			},
		},
	}
	d := drivers.NewACMDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "example.com"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.ProviderID != certARN {
		t.Errorf("expected %s, got %s", certARN, out.ProviderID)
	}
}

func TestACMDriver_Delete_ByProviderID(t *testing.T) {
	d := drivers.NewACMDriverWithClient(&mockACMClient{})
	err := d.Delete(context.Background(), interfaces.ResourceRef{
		Name:       "example.com",
		ProviderID: "arn:aws:acm:us-east-1:123:certificate/abc-123",
	})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestACMDriver_HealthCheck_Issued(t *testing.T) {
	certARN := "arn:aws:acm:us-east-1:123:certificate/abc-123"
	mock := &mockACMClient{
		describeOut: &acm.DescribeCertificateOutput{
			Certificate: &acmtypes.CertificateDetail{
				CertificateArn: awssdk.String(certARN),
				DomainName:     awssdk.String("example.com"),
				Status:         acmtypes.CertificateStatusIssued,
			},
		},
	}
	d := drivers.NewACMDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{
		Name:       "example.com",
		ProviderID: certARN,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy for ISSUED cert")
	}
}

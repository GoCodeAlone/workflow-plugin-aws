package drivers_test

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockRoute53Client struct {
	createOut   *route53.CreateHostedZoneOutput
	createErr   error
	listOut     *route53.ListHostedZonesByNameOutput
	listErr     error
	getOut      *route53.GetHostedZoneOutput
	getErr      error
	deleteErr   error
	changeErr   error
}

func (m *mockRoute53Client) CreateHostedZone(_ context.Context, _ *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockRoute53Client) ListHostedZonesByName(_ context.Context, _ *route53.ListHostedZonesByNameInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
	return m.listOut, m.listErr
}
func (m *mockRoute53Client) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return m.getOut, m.getErr
}
func (m *mockRoute53Client) DeleteHostedZone(_ context.Context, _ *route53.DeleteHostedZoneInput, _ ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error) {
	return &route53.DeleteHostedZoneOutput{}, m.deleteErr
}
func (m *mockRoute53Client) ChangeResourceRecordSets(_ context.Context, _ *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return &route53.ChangeResourceRecordSetsOutput{}, m.changeErr
}

func TestRoute53Driver_Create(t *testing.T) {
	mock := &mockRoute53Client{
		createOut: &route53.CreateHostedZoneOutput{
			HostedZone: &r53types.HostedZone{
				Id:   awssdk.String("/hostedzone/Z123456"),
				Name: awssdk.String("example.com."),
			},
		},
	}
	d := drivers.NewRoute53DriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "example.com",
		Config: map[string]any{"domain_name": "example.com"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.dns" {
		t.Errorf("expected infra.dns, got %s", out.Type)
	}
	if out.ProviderID != "/hostedzone/Z123456" {
		t.Errorf("unexpected ProviderID: %s", out.ProviderID)
	}
}

func TestRoute53Driver_Read_ByName(t *testing.T) {
	mock := &mockRoute53Client{
		listOut: &route53.ListHostedZonesByNameOutput{
			HostedZones: []r53types.HostedZone{
				{
					Id:   awssdk.String("/hostedzone/Z123456"),
					Name: awssdk.String("example.com."),
				},
			},
		},
	}
	d := drivers.NewRoute53DriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "example.com"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Outputs["zone_id"] != "/hostedzone/Z123456" {
		t.Errorf("unexpected zone_id: %v", out.Outputs["zone_id"])
	}
}

func TestRoute53Driver_Delete(t *testing.T) {
	mock := &mockRoute53Client{
		listOut: &route53.ListHostedZonesByNameOutput{
			HostedZones: []r53types.HostedZone{
				{Id: awssdk.String("/hostedzone/Z123456"), Name: awssdk.String("example.com.")},
			},
		},
	}
	d := drivers.NewRoute53DriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "example.com"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestRoute53Driver_HealthCheck(t *testing.T) {
	mock := &mockRoute53Client{
		listOut: &route53.ListHostedZonesByNameOutput{
			HostedZones: []r53types.HostedZone{
				{Id: awssdk.String("/hostedzone/Z123456"), Name: awssdk.String("example.com.")},
			},
		},
	}
	d := drivers.NewRoute53DriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy")
	}
}

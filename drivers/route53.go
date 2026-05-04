package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// Route53Client is the subset of Route53 API used by Route53Driver.
type Route53Client interface {
	CreateHostedZone(ctx context.Context, params *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
	ListHostedZonesByName(ctx context.Context, params *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
	GetHostedZone(ctx context.Context, params *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	DeleteHostedZone(ctx context.Context, params *route53.DeleteHostedZoneInput, optFns ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error)
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

// Route53Driver manages Route53 hosted zones (infra.dns).
type Route53Driver struct {
	client Route53Client
}

// NewRoute53Driver creates a Route53 driver from an AWS config.
func NewRoute53Driver(cfg awssdk.Config) *Route53Driver {
	return &Route53Driver{client: route53.NewFromConfig(cfg)}
}

// NewRoute53DriverWithClient creates a Route53 driver with a custom client (for tests).
func NewRoute53DriverWithClient(client Route53Client) *Route53Driver {
	return &Route53Driver{client: client}
}

func (d *Route53Driver) ResourceType() string { return "infra.dns" }

func (d *Route53Driver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	domainName, _ := spec.Config["domain_name"].(string)
	if domainName == "" {
		domainName = spec.Name
	}
	comment, _ := spec.Config["comment"].(string)
	private := boolProp(spec.Config, "private", false)

	in := &route53.CreateHostedZoneInput{
		Name:            awssdk.String(domainName),
		CallerReference: awssdk.String(fmt.Sprintf("workflow-%s", spec.Name)),
		HostedZoneConfig: &r53types.HostedZoneConfig{
			Comment:     awssdk.String(comment),
			PrivateZone: private,
		},
	}

	out, err := d.client.CreateHostedZone(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("route53: create zone %q: %w", spec.Name, err)
	}
	return r53ZoneToOutput(spec.Name, out.HostedZone), nil
}

func (d *Route53Driver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if ref.ProviderID != "" {
		out, err := d.client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: awssdk.String(ref.ProviderID)})
		if err != nil {
			return nil, fmt.Errorf("route53: get zone %q: %w", ref.ProviderID, err)
		}
		return r53ZoneToOutput(ref.Name, out.HostedZone), nil
	}

	domainName := ref.Name
	out, err := d.client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName: awssdk.String(domainName),
	})
	if err != nil {
		return nil, fmt.Errorf("route53: list zones by name %q: %w", domainName, err)
	}
	if len(out.HostedZones) == 0 {
		return nil, fmt.Errorf("route53: zone %q not found", domainName)
	}
	return r53ZoneToOutput(ref.Name, &out.HostedZones[0]), nil
}

func (d *Route53Driver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	// Update individual records if specified
	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	zoneID, _ := current.Outputs["zone_id"].(string)
	if zoneID == "" {
		return nil, fmt.Errorf("route53: update %q: no zone_id", ref.Name)
	}

	records, _ := spec.Config["records"].([]any)
	if len(records) > 0 {
		var changes []r53types.Change
		for _, r := range records {
			rec, ok := r.(map[string]any)
			if !ok {
				continue
			}
			name, _ := rec["name"].(string)
			rtype, _ := rec["type"].(string)
			ttl := int64(intProp(rec, "ttl", 300))
			values := stringSliceProp(rec, "values")
			var rrs []r53types.ResourceRecord
			for _, v := range values {
				rrs = append(rrs, r53types.ResourceRecord{Value: awssdk.String(v)})
			}
			changes = append(changes, r53types.Change{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: awssdk.String(name),
					Type: r53types.RRType(rtype),
					TTL:  awssdk.Int64(ttl),
					ResourceRecords: rrs,
				},
			})
		}
		if len(changes) > 0 {
			_, err = d.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
				HostedZoneId: awssdk.String(zoneID),
				ChangeBatch:  &r53types.ChangeBatch{Changes: changes},
			})
			if err != nil {
				return nil, fmt.Errorf("route53: update records %q: %w", ref.Name, err)
			}
		}
	}
	return d.Read(ctx, ref)
}

func (d *Route53Driver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return err
	}
	zoneID, _ := current.Outputs["zone_id"].(string)
	if zoneID == "" {
		return fmt.Errorf("route53: delete %q: no zone_id", ref.Name)
	}
	_, err = d.client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: awssdk.String(zoneID)})
	if err != nil {
		return fmt.Errorf("route53: delete zone %q: %w", ref.Name, err)
	}
	return nil
}

func (d *Route53Driver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *Route53Driver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "zone exists"}, nil
}

func (d *Route53Driver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("route53: DNS zones are not scalable")
}

func r53ZoneToOutput(name string, zone *r53types.HostedZone) *interfaces.ResourceOutput {
	if zone == nil {
		return nil
	}
	outputs := map[string]any{}
	if zone.Id != nil {
		outputs["zone_id"] = *zone.Id
	}
	if zone.Name != nil {
		outputs["domain_name"] = *zone.Name
	}
	if zone.Config != nil {
		outputs["private"] = zone.Config.PrivateZone
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.dns",
		ProviderID: awssdk.ToString(zone.Id),
		Outputs:    outputs,
		Status:     "running",
	}
}

// SensitiveKeys returns output keys whose values should be masked in logs and plan output.
func (d *Route53Driver) SensitiveKeys() []string { return nil }

var _ interfaces.ResourceDriver = (*Route53Driver)(nil)

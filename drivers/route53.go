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
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	DeleteHostedZone(ctx context.Context, params *route53.DeleteHostedZoneInput, optFns ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error)
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
}

// Route53Driver manages Route53 hosted zones (infra.dns).
type Route53Driver struct {
	noSensitiveKeys
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
	return r53ZoneToOutput(spec.Name, out.HostedZone, out.DelegationSet, nil), nil
}

func (d *Route53Driver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if ref.ProviderID != "" {
		out, err := d.client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: awssdk.String(ref.ProviderID)})
		if err != nil {
			return nil, fmt.Errorf("route53: get zone %q: %w", ref.ProviderID, err)
		}
		records, err := d.listRecordSets(ctx, ref.ProviderID)
		if err != nil {
			return nil, err
		}
		return r53ZoneToOutput(ref.Name, out.HostedZone, out.DelegationSet, records), nil
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
	zoneID := awssdk.ToString(out.HostedZones[0].Id)
	hostedZone := &out.HostedZones[0]
	zone, err := d.client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: awssdk.String(zoneID)})
	if err != nil {
		return nil, fmt.Errorf("route53: get zone %q: %w", zoneID, err)
	}
	var delegation *r53types.DelegationSet
	if zone != nil {
		if zone.HostedZone != nil {
			hostedZone = zone.HostedZone
		}
		delegation = zone.DelegationSet
	}
	records, err := d.listRecordSets(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	return r53ZoneToOutput(ref.Name, hostedZone, delegation, records), nil
}

func (d *Route53Driver) listRecordSets(ctx context.Context, zoneID string) ([]r53types.ResourceRecordSet, error) {
	out, err := d.client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{HostedZoneId: awssdk.String(zoneID)})
	if err != nil {
		return nil, fmt.Errorf("route53: list records %q: %w", zoneID, err)
	}
	if out == nil {
		return nil, nil
	}
	return out.ResourceRecordSets, nil
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
					Name:            awssdk.String(name),
					Type:            r53types.RRType(rtype),
					TTL:             awssdk.Int64(ttl),
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

func r53ZoneToOutput(name string, zone *r53types.HostedZone, delegation *r53types.DelegationSet, records []r53types.ResourceRecordSet) *interfaces.ResourceOutput {
	if zone == nil {
		return nil
	}
	outputs := map[string]any{}
	if zone.Id != nil {
		outputs["zone_id"] = *zone.Id
	}
	if zone.Name != nil {
		outputs["domain_name"] = *zone.Name
		outputs["domain"] = *zone.Name
	}
	if zone.Config != nil {
		outputs["private"] = zone.Config.PrivateZone
	}
	if delegation != nil {
		outputs["name_servers"] = append([]string(nil), delegation.NameServers...)
	}
	if records != nil {
		outputs["records"] = r53RecordOutputs(records)
		outputs["record_count"] = len(records)
	} else if zone.ResourceRecordSetCount != nil {
		outputs["record_count"] = int(*zone.ResourceRecordSetCount)
	}
	outputs["authority"] = map[string]any{
		"role":         "target_authoritative_dns",
		"dns_host":     "Route53",
		"name_servers": append([]string(nil), delegationNameServers(delegation)...),
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.dns",
		ProviderID: awssdk.ToString(zone.Id),
		Outputs:    outputs,
		Status:     "running",
	}
}

func delegationNameServers(delegation *r53types.DelegationSet) []string {
	if delegation == nil {
		return nil
	}
	return delegation.NameServers
}

func r53RecordOutputs(records []r53types.ResourceRecordSet) []map[string]any {
	outputs := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out := map[string]any{
			"name": awssdk.ToString(record.Name),
			"type": string(record.Type),
		}
		if record.TTL != nil {
			out["ttl"] = *record.TTL
		}
		if len(record.ResourceRecords) > 0 {
			values := make([]string, 0, len(record.ResourceRecords))
			for _, value := range record.ResourceRecords {
				values = append(values, awssdk.ToString(value.Value))
			}
			out["values"] = values
		}
		if record.AliasTarget != nil {
			out["alias_target"] = map[string]any{
				"dns_name":               awssdk.ToString(record.AliasTarget.DNSName),
				"hosted_zone_id":         awssdk.ToString(record.AliasTarget.HostedZoneId),
				"evaluate_target_health": record.AliasTarget.EvaluateTargetHealth,
			}
		}
		outputs = append(outputs, out)
	}
	return outputs
}

// SensitiveKeys returns output keys whose values should be masked in logs and plan output.
func (d *Route53Driver) SensitiveKeys() []string { return nil }

var _ interfaces.ResourceDriver = (*Route53Driver)(nil)

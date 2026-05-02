package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// ELBv2Client is the subset of ELBv2 API used by ALBDriver.
type ELBv2Client interface {
	CreateLoadBalancer(ctx context.Context, params *elbv2.CreateLoadBalancerInput, optFns ...func(*elbv2.Options)) (*elbv2.CreateLoadBalancerOutput, error)
	DescribeLoadBalancers(ctx context.Context, params *elbv2.DescribeLoadBalancersInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error)
	DeleteLoadBalancer(ctx context.Context, params *elbv2.DeleteLoadBalancerInput, optFns ...func(*elbv2.Options)) (*elbv2.DeleteLoadBalancerOutput, error)
	ModifyLoadBalancerAttributes(ctx context.Context, params *elbv2.ModifyLoadBalancerAttributesInput, optFns ...func(*elbv2.Options)) (*elbv2.ModifyLoadBalancerAttributesOutput, error)
}

// ALBDriver manages Application/Network Load Balancers (infra.load_balancer).
type ALBDriver struct {
	noSensitiveKeys
	client ELBv2Client
}

// NewALBDriver creates an ALB driver from an AWS config.
func NewALBDriver(cfg awssdk.Config) *ALBDriver {
	return &ALBDriver{client: elbv2.NewFromConfig(cfg)}
}

// NewALBDriverWithClient creates an ALB driver with a custom client (for tests).
func NewALBDriverWithClient(client ELBv2Client) *ALBDriver {
	return &ALBDriver{client: client}
}

func (d *ALBDriver) ResourceType() string { return "infra.load_balancer" }

func (d *ALBDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	scheme, _ := spec.Config["scheme"].(string)
	if scheme == "" {
		scheme = "internet-facing"
	}
	lbType, _ := spec.Config["type"].(string)

	lbScheme := elbtypes.LoadBalancerSchemeEnumInternetFacing
	if scheme == "internal" {
		lbScheme = elbtypes.LoadBalancerSchemeEnumInternal
	}
	lbTypeEnum := elbtypes.LoadBalancerTypeEnumApplication
	if lbType == "network" {
		lbTypeEnum = elbtypes.LoadBalancerTypeEnumNetwork
	}

	in := &elbv2.CreateLoadBalancerInput{
		Name:           awssdk.String(spec.Name),
		Scheme:         lbScheme,
		Type:           lbTypeEnum,
		Subnets:        stringSliceProp(spec.Config, "subnet_ids"),
		SecurityGroups: stringSliceProp(spec.Config, "security_group_ids"),
	}

	out, err := d.client.CreateLoadBalancer(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("alb: create %q: %w", spec.Name, err)
	}
	if len(out.LoadBalancers) == 0 {
		return nil, fmt.Errorf("alb: create %q returned no load balancers", spec.Name)
	}
	return albLBToOutput(&out.LoadBalancers[0]), nil
}

func (d *ALBDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	in := &elbv2.DescribeLoadBalancersInput{}
	if ref.ProviderID != "" {
		in.LoadBalancerArns = []string{ref.ProviderID}
	} else {
		in.Names = []string{ref.Name}
	}
	out, err := d.client.DescribeLoadBalancers(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("alb: describe %q: %w", ref.Name, err)
	}
	if len(out.LoadBalancers) == 0 {
		return nil, fmt.Errorf("alb: %q not found", ref.Name)
	}
	return albLBToOutput(&out.LoadBalancers[0]), nil
}

func (d *ALBDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	arn, _ := current.Outputs["arn"].(string)
	if arn == "" {
		return nil, fmt.Errorf("alb: update %q: missing ARN", ref.Name)
	}

	var attrs []elbtypes.LoadBalancerAttribute
	if idle, _ := spec.Config["idle_timeout"].(string); idle != "" {
		attrs = append(attrs, elbtypes.LoadBalancerAttribute{
			Key:   awssdk.String("idle_timeout.timeout_seconds"),
			Value: awssdk.String(idle),
		})
	}
	if len(attrs) > 0 {
		_, err := d.client.ModifyLoadBalancerAttributes(ctx, &elbv2.ModifyLoadBalancerAttributesInput{
			LoadBalancerArn: awssdk.String(arn),
			Attributes:      attrs,
		})
		if err != nil {
			return nil, fmt.Errorf("alb: modify %q: %w", ref.Name, err)
		}
	}
	return d.Read(ctx, ref)
}

func (d *ALBDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return err
	}
	arn, _ := current.Outputs["arn"].(string)
	if arn == "" {
		return fmt.Errorf("alb: delete %q: missing ARN", ref.Name)
	}
	_, err = d.client.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: awssdk.String(arn),
	})
	if err != nil {
		return fmt.Errorf("alb: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *ALBDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *ALBDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	healthy := out.Status == "running"
	return &interfaces.HealthResult{Healthy: healthy, Message: out.Status}, nil
}

func (d *ALBDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("alb: load balancers scale automatically")
}

func albLBToOutput(lb *elbtypes.LoadBalancer) *interfaces.ResourceOutput {
	if lb == nil {
		return nil
	}
	status := "running"
	if lb.State != nil {
		switch lb.State.Code {
		case elbtypes.LoadBalancerStateEnumProvisioning:
			status = "creating"
		case elbtypes.LoadBalancerStateEnumFailed:
			status = "failed"
		}
	}

	outputs := map[string]any{
		"scheme": string(lb.Scheme),
		"type":   string(lb.Type),
	}
	if lb.LoadBalancerArn != nil {
		outputs["arn"] = *lb.LoadBalancerArn
	}
	if lb.DNSName != nil {
		outputs["dns_name"] = *lb.DNSName
		outputs["endpoint"] = *lb.DNSName
	}
	if lb.VpcId != nil {
		outputs["vpc_id"] = *lb.VpcId
	}

	name := awssdk.ToString(lb.LoadBalancerName)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.load_balancer",
		ProviderID: awssdk.ToString(lb.LoadBalancerArn),
		Outputs:    outputs,
		Status:     status,
	}
}

var _ interfaces.ResourceDriver = (*ALBDriver)(nil)

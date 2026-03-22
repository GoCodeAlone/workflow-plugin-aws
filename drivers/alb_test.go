package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockELBv2Client struct {
	createOut  *elbv2.CreateLoadBalancerOutput
	createErr  error
	describeOut *elbv2.DescribeLoadBalancersOutput
	describeErr error
	deleteErr   error
	modifyErr   error
}

func (m *mockELBv2Client) CreateLoadBalancer(_ context.Context, _ *elbv2.CreateLoadBalancerInput, _ ...func(*elbv2.Options)) (*elbv2.CreateLoadBalancerOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockELBv2Client) DescribeLoadBalancers(_ context.Context, _ *elbv2.DescribeLoadBalancersInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error) {
	return m.describeOut, m.describeErr
}
func (m *mockELBv2Client) DeleteLoadBalancer(_ context.Context, _ *elbv2.DeleteLoadBalancerInput, _ ...func(*elbv2.Options)) (*elbv2.DeleteLoadBalancerOutput, error) {
	return &elbv2.DeleteLoadBalancerOutput{}, m.deleteErr
}
func (m *mockELBv2Client) ModifyLoadBalancerAttributes(_ context.Context, _ *elbv2.ModifyLoadBalancerAttributesInput, _ ...func(*elbv2.Options)) (*elbv2.ModifyLoadBalancerAttributesOutput, error) {
	return &elbv2.ModifyLoadBalancerAttributesOutput{}, m.modifyErr
}

func TestALBDriver_Create(t *testing.T) {
	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/my-alb/abc123"
	mock := &mockELBv2Client{
		createOut: &elbv2.CreateLoadBalancerOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String(lbARN),
					LoadBalancerName: awssdk.String("my-alb"),
					DNSName:          awssdk.String("my-alb.us-east-1.elb.amazonaws.com"),
					Scheme:           elbtypes.LoadBalancerSchemeEnumInternetFacing,
					Type:             elbtypes.LoadBalancerTypeEnumApplication,
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"scheme": "internet-facing"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.load_balancer" {
		t.Errorf("expected infra.load_balancer, got %s", out.Type)
	}
	if out.ProviderID != lbARN {
		t.Errorf("expected ProviderID %s, got %s", lbARN, out.ProviderID)
	}
}

func TestALBDriver_Read(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:..."),
					LoadBalancerName: awssdk.String("my-alb"),
					DNSName:          awssdk.String("my-alb.example.com"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-alb"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Name != "my-alb" {
		t.Errorf("expected my-alb, got %s", out.Name)
	}
}

func TestALBDriver_Delete(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-alb"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestALBDriver_Scale_ReturnsError(t *testing.T) {
	d := drivers.NewALBDriverWithClient(&mockELBv2Client{})
	_, err := d.Scale(context.Background(), interfaces.ResourceRef{Name: "my-alb"}, 3)
	if err == nil {
		t.Error("expected error from Scale on ALB")
	}
}

func TestALBDriver_Create_Error(t *testing.T) {
	mock := &mockELBv2Client{createErr: fmt.Errorf("load balancer limit exceeded")}
	d := drivers.NewALBDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"scheme": "internet-facing"},
	})
	if err == nil {
		t.Fatal("expected error on CreateLoadBalancer API failure")
	}
}

func TestALBDriver_Update_Success(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-alb"}, interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"idle_timeout": 120},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestALBDriver_Update_Error(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
		modifyErr: fmt.Errorf("invalid attribute"),
	}
	d := drivers.NewALBDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-alb"}, interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"idle_timeout": "-1"},
	})
	if err == nil {
		t.Fatal("expected error on ModifyLoadBalancerAttributes API failure")
	}
}

func TestALBDriver_Delete_Error(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
		deleteErr: fmt.Errorf("load balancer not found"),
	}
	d := drivers.NewALBDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-alb"})
	if err == nil {
		t.Fatal("expected error on DeleteLoadBalancer API failure")
	}
}

func TestALBDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewALBDriverWithClient(&mockELBv2Client{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "my-alb"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestALBDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewALBDriverWithClient(&mockELBv2Client{})
	current := &interfaces.ResourceOutput{
		Name:    "my-alb",
		Type:    "infra.load_balancer",
		Outputs: map[string]any{"scheme": "internal"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"scheme": "internet-facing"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when scheme changes")
	}
}

func TestALBDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewALBDriverWithClient(&mockELBv2Client{})
	current := &interfaces.ResourceOutput{
		Name:    "my-alb",
		Type:    "infra.load_balancer",
		Outputs: map[string]any{"scheme": "internet-facing"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-alb",
		Config: map[string]any{"scheme": "internet-facing"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestALBDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumActive},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-alb"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy, got: %s", h.Message)
	}
}

func TestALBDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockELBv2Client{
		describeOut: &elbv2.DescribeLoadBalancersOutput{
			LoadBalancers: []elbtypes.LoadBalancer{
				{
					LoadBalancerArn:  awssdk.String("arn:aws:elb:us-east-1:123:lb/my-alb"),
					LoadBalancerName: awssdk.String("my-alb"),
					State:            &elbtypes.LoadBalancerState{Code: elbtypes.LoadBalancerStateEnumProvisioning},
				},
			},
		},
	}
	d := drivers.NewALBDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-alb"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy for provisioning ALB")
	}
}

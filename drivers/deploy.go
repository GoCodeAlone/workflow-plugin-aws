package drivers

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	wfmodule "github.com/GoCodeAlone/workflow/module"
)

// ECSDeployClient extends ECSClient with task definition query support.
type ECSDeployClient interface {
	ECSClient
	DescribeTaskDefinition(ctx context.Context, params *ecs.DescribeTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
}

// DeployELBv2Client covers ALB operations needed for blue/green and canary routing.
type DeployELBv2Client interface {
	CreateTargetGroup(ctx context.Context, params *elbv2.CreateTargetGroupInput, optFns ...func(*elbv2.Options)) (*elbv2.CreateTargetGroupOutput, error)
	DeleteTargetGroup(ctx context.Context, params *elbv2.DeleteTargetGroupInput, optFns ...func(*elbv2.Options)) (*elbv2.DeleteTargetGroupOutput, error)
	ModifyListener(ctx context.Context, params *elbv2.ModifyListenerInput, optFns ...func(*elbv2.Options)) (*elbv2.ModifyListenerOutput, error)
}

// CloudWatchMetricClient covers the CloudWatch calls needed for metric gates.
type CloudWatchMetricClient interface {
	GetMetricStatistics(ctx context.Context, params *cloudwatch.GetMetricStatisticsInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricStatisticsOutput, error)
}

// ECSDeployConfig holds static configuration for the ECS deploy driver.
type ECSDeployConfig struct {
	Cluster         string
	ServiceName     string
	ListenerARN     string  // ALB listener ARN for traffic routing
	StableTGARN     string  // ARN of the stable (blue) target group
	ALBDNS          string  // ALB DNS name returned as green endpoint
	MetricNamespace string  // CloudWatch namespace for metric gates (default: "ECS/DeployMetrics")
	MetricThreshold float64 // Error rate threshold for metric gates (default: 0.05)
}

// ECSDeployDriver implements DeployDriver, BlueGreenDriver, and CanaryDriver
// against Amazon ECS + ALB.
type ECSDeployDriver struct {
	ecs  ECSDeployClient
	elb  DeployELBv2Client
	cw   CloudWatchMetricClient

	cluster     string
	serviceName string
	listenerARN string
	stableTGARN string
	albDNS      string

	metricNamespace string
	metricThreshold float64

	// transient blue/green state
	greenServiceName string
	greenTGARN       string

	// transient canary state
	canaryServiceName string
	canaryTGARN       string
}

// NewECSDeployDriver creates an ECSDeployDriver from an AWS config.
func NewECSDeployDriver(cfg awssdk.Config, deployCfg ECSDeployConfig) *ECSDeployDriver {
	ns := deployCfg.MetricNamespace
	if ns == "" {
		ns = "ECS/DeployMetrics"
	}
	threshold := deployCfg.MetricThreshold
	if threshold == 0 {
		threshold = 0.05
	}
	return &ECSDeployDriver{
		ecs:             ecs.NewFromConfig(cfg),
		elb:             elbv2.NewFromConfig(cfg),
		cw:              cloudwatch.NewFromConfig(cfg),
		cluster:         deployCfg.Cluster,
		serviceName:     deployCfg.ServiceName,
		listenerARN:     deployCfg.ListenerARN,
		stableTGARN:     deployCfg.StableTGARN,
		albDNS:          deployCfg.ALBDNS,
		metricNamespace: ns,
		metricThreshold: threshold,
	}
}

// NewECSDeployDriverWithClients creates an ECSDeployDriver with custom clients (for tests).
func NewECSDeployDriverWithClients(ecsClient ECSDeployClient, elbClient DeployELBv2Client, cwClient CloudWatchMetricClient, deployCfg ECSDeployConfig) *ECSDeployDriver {
	ns := deployCfg.MetricNamespace
	if ns == "" {
		ns = "ECS/DeployMetrics"
	}
	threshold := deployCfg.MetricThreshold
	if threshold == 0 {
		threshold = 0.05
	}
	return &ECSDeployDriver{
		ecs:             ecsClient,
		elb:             elbClient,
		cw:              cwClient,
		cluster:         deployCfg.Cluster,
		serviceName:     deployCfg.ServiceName,
		listenerARN:     deployCfg.ListenerARN,
		stableTGARN:     deployCfg.StableTGARN,
		albDNS:          deployCfg.ALBDNS,
		metricNamespace: ns,
		metricThreshold: threshold,
	}
}

// activeService returns the service to target for health checks:
// the green service during blue/green, otherwise the stable service.
func (d *ECSDeployDriver) activeService() string {
	if d.greenServiceName != "" {
		return d.greenServiceName
	}
	return d.serviceName
}

// ─── DeployDriver ─────────────────────────────────────────────────────────────

// Update registers a new task definition revision with the given image and
// updates the ECS service to use it.
func (d *ECSDeployDriver) Update(ctx context.Context, image string) error {
	tdOut, err := d.ecs.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  awssdk.String(d.serviceName),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{Name: awssdk.String(d.serviceName), Image: awssdk.String(image), Essential: awssdk.Bool(true)},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: register task def %q: %w", d.serviceName, err)
	}
	tdARN := awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn)

	_, err = d.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:        awssdk.String(d.cluster),
		Service:        awssdk.String(d.serviceName),
		TaskDefinition: awssdk.String(tdARN),
	})
	if err != nil {
		return fmt.Errorf("deploy: update service %q: %w", d.serviceName, err)
	}
	return nil
}

// HealthCheck checks that the active ECS service has running >= desired tasks
// and no failed deployments. The path parameter is accepted for interface
// compatibility but ECS health is checked via service state, not HTTP.
func (d *ECSDeployDriver) HealthCheck(ctx context.Context, _ string) error {
	svcName := d.activeService()
	out, err := d.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(d.cluster),
		Services: []string{svcName},
	})
	if err != nil {
		return fmt.Errorf("deploy: health check %q: %w", svcName, err)
	}
	if len(out.Services) == 0 {
		return fmt.Errorf("deploy: health check %q: service not found", svcName)
	}
	svc := out.Services[0]
	if svc.RunningCount < svc.DesiredCount {
		return fmt.Errorf("deploy: health check %q: running=%d desired=%d", svcName, svc.RunningCount, svc.DesiredCount)
	}
	for _, dep := range svc.Deployments {
		if dep.RolloutState == ecstypes.DeploymentRolloutStateFailed {
			return fmt.Errorf("deploy: health check %q: deployment rollout failed", svcName)
		}
	}
	return nil
}

// CurrentImage returns the container image currently running for the stable service.
func (d *ECSDeployDriver) CurrentImage(ctx context.Context) (string, error) {
	descOut, err := d.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(d.cluster),
		Services: []string{d.serviceName},
	})
	if err != nil {
		return "", fmt.Errorf("deploy: describe service %q: %w", d.serviceName, err)
	}
	if len(descOut.Services) == 0 {
		return "", fmt.Errorf("deploy: service %q not found", d.serviceName)
	}
	tdARN := awssdk.ToString(descOut.Services[0].TaskDefinition)

	tdOut, err := d.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: awssdk.String(tdARN),
	})
	if err != nil {
		return "", fmt.Errorf("deploy: describe task def %q: %w", tdARN, err)
	}
	if tdOut.TaskDefinition == nil || len(tdOut.TaskDefinition.ContainerDefinitions) == 0 {
		return "", fmt.Errorf("deploy: task def %q has no containers", tdARN)
	}
	return awssdk.ToString(tdOut.TaskDefinition.ContainerDefinitions[0].Image), nil
}

// ReplicaCount returns the desired replica count for the stable service.
func (d *ECSDeployDriver) ReplicaCount(ctx context.Context) (int, error) {
	out, err := d.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(d.cluster),
		Services: []string{d.serviceName},
	})
	if err != nil {
		return 0, fmt.Errorf("deploy: describe service %q: %w", d.serviceName, err)
	}
	if len(out.Services) == 0 {
		return 0, fmt.Errorf("deploy: service %q not found", d.serviceName)
	}
	return int(out.Services[0].DesiredCount), nil
}

// ─── BlueGreenDriver ──────────────────────────────────────────────────────────

// CreateGreen creates a green ECS service with the given image, a new target
// group, and registers it with the ALB listener.
func (d *ECSDeployDriver) CreateGreen(ctx context.Context, image string) error {
	greenSvcName := d.serviceName + "-green"
	greenTGName := d.serviceName + "-green-tg"

	// Register task definition for the green service.
	tdOut, err := d.ecs.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  awssdk.String(greenSvcName),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{Name: awssdk.String(greenSvcName), Image: awssdk.String(image), Essential: awssdk.Bool(true)},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: green register task def: %w", err)
	}
	tdARN := awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn)

	// Create a new target group for the green service.
	tgOut, err := d.elb.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:       awssdk.String(greenTGName),
		Protocol:   elbtypes.ProtocolEnumHttp,
		Port:       awssdk.Int32(80),
		TargetType: elbtypes.TargetTypeEnumIp,
	})
	if err != nil {
		return fmt.Errorf("deploy: create green target group: %w", err)
	}
	if len(tgOut.TargetGroups) == 0 {
		return fmt.Errorf("deploy: create green target group returned empty result")
	}
	d.greenTGARN = awssdk.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	// Create the green ECS service.
	_, err = d.ecs.CreateService(ctx, &ecs.CreateServiceInput{
		Cluster:        awssdk.String(d.cluster),
		ServiceName:    awssdk.String(greenSvcName),
		TaskDefinition: awssdk.String(tdARN),
		DesiredCount:   awssdk.Int32(1),
		LaunchType:     ecstypes.LaunchTypeFargate,
		LoadBalancers: []ecstypes.LoadBalancer{
			{
				TargetGroupArn: awssdk.String(d.greenTGARN),
				ContainerName:  awssdk.String(greenSvcName),
				ContainerPort:  awssdk.Int32(80),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: create green service: %w", err)
	}
	d.greenServiceName = greenSvcName
	return nil
}

// SwitchTraffic routes all ALB listener traffic to the green target group.
func (d *ECSDeployDriver) SwitchTraffic(ctx context.Context) error {
	if d.greenTGARN == "" {
		return fmt.Errorf("deploy: switch traffic: no green target group (CreateGreen not called)")
	}
	_, err := d.elb.ModifyListener(ctx, &elbv2.ModifyListenerInput{
		ListenerArn: awssdk.String(d.listenerARN),
		DefaultActions: []elbtypes.Action{
			{
				Type: elbtypes.ActionTypeEnumForward,
				ForwardConfig: &elbtypes.ForwardActionConfig{
					TargetGroups: []elbtypes.TargetGroupTuple{
						{TargetGroupArn: awssdk.String(d.greenTGARN), Weight: awssdk.Int32(1)},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: switch traffic to green: %w", err)
	}
	return nil
}

// DestroyBlue tears down the blue ECS service and its target group, then
// promotes green to stable.
func (d *ECSDeployDriver) DestroyBlue(ctx context.Context) error {
	// Scale to 0 before delete to drain connections.
	_, _ = d.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      awssdk.String(d.cluster),
		Service:      awssdk.String(d.serviceName),
		DesiredCount: awssdk.Int32(0),
	})
	if _, err := d.ecs.DeleteService(ctx, &ecs.DeleteServiceInput{
		Cluster: awssdk.String(d.cluster),
		Service: awssdk.String(d.serviceName),
		Force:   awssdk.Bool(true),
	}); err != nil {
		return fmt.Errorf("deploy: delete blue service %q: %w", d.serviceName, err)
	}

	if d.stableTGARN != "" {
		if _, err := d.elb.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
			TargetGroupArn: awssdk.String(d.stableTGARN),
		}); err != nil {
			return fmt.Errorf("deploy: delete blue target group: %w", err)
		}
	}

	// Promote green → stable.
	d.serviceName = d.greenServiceName
	d.stableTGARN = d.greenTGARN
	d.greenServiceName = ""
	d.greenTGARN = ""
	return nil
}

// GreenEndpoint returns the ALB DNS name as the green environment endpoint.
func (d *ECSDeployDriver) GreenEndpoint(_ context.Context) (string, error) {
	if d.albDNS == "" {
		return "", fmt.Errorf("deploy: green endpoint: ALB DNS not configured")
	}
	return d.albDNS, nil
}

// ─── CanaryDriver ─────────────────────────────────────────────────────────────

// CreateCanary creates a canary ECS service with 1 replica and a new target group.
func (d *ECSDeployDriver) CreateCanary(ctx context.Context, image string) error {
	canarySvcName := d.serviceName + "-canary"
	canaryTGName := d.serviceName + "-canary-tg"

	tdOut, err := d.ecs.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  awssdk.String(canarySvcName),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{Name: awssdk.String(canarySvcName), Image: awssdk.String(image), Essential: awssdk.Bool(true)},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: canary register task def: %w", err)
	}
	tdARN := awssdk.ToString(tdOut.TaskDefinition.TaskDefinitionArn)

	tgOut, err := d.elb.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:       awssdk.String(canaryTGName),
		Protocol:   elbtypes.ProtocolEnumHttp,
		Port:       awssdk.Int32(80),
		TargetType: elbtypes.TargetTypeEnumIp,
	})
	if err != nil {
		return fmt.Errorf("deploy: create canary target group: %w", err)
	}
	if len(tgOut.TargetGroups) == 0 {
		return fmt.Errorf("deploy: create canary target group returned empty result")
	}
	d.canaryTGARN = awssdk.ToString(tgOut.TargetGroups[0].TargetGroupArn)

	_, err = d.ecs.CreateService(ctx, &ecs.CreateServiceInput{
		Cluster:        awssdk.String(d.cluster),
		ServiceName:    awssdk.String(canarySvcName),
		TaskDefinition: awssdk.String(tdARN),
		DesiredCount:   awssdk.Int32(1),
		LaunchType:     ecstypes.LaunchTypeFargate,
		LoadBalancers: []ecstypes.LoadBalancer{
			{
				TargetGroupArn: awssdk.String(d.canaryTGARN),
				ContainerName:  awssdk.String(canarySvcName),
				ContainerPort:  awssdk.Int32(80),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: create canary service: %w", err)
	}
	d.canaryServiceName = canarySvcName
	return nil
}

// RoutePercent shifts the given percentage of traffic to the canary target group.
func (d *ECSDeployDriver) RoutePercent(ctx context.Context, percent int) error {
	if d.canaryTGARN == "" {
		return fmt.Errorf("deploy: route percent: no canary target group (CreateCanary not called)")
	}
	_, err := d.elb.ModifyListener(ctx, &elbv2.ModifyListenerInput{
		ListenerArn: awssdk.String(d.listenerARN),
		DefaultActions: []elbtypes.Action{
			{
				Type: elbtypes.ActionTypeEnumForward,
				ForwardConfig: &elbtypes.ForwardActionConfig{
					TargetGroups: []elbtypes.TargetGroupTuple{
						{TargetGroupArn: awssdk.String(d.canaryTGARN), Weight: awssdk.Int32(int32(percent))},
						{TargetGroupArn: awssdk.String(d.stableTGARN), Weight: awssdk.Int32(int32(100 - percent))},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("deploy: route %d%% to canary: %w", percent, err)
	}
	return nil
}

// CheckMetricGate queries CloudWatch for the named metric over the last 5 minutes.
// Returns an error if the average exceeds the configured threshold.
func (d *ECSDeployDriver) CheckMetricGate(ctx context.Context, gate string) error {
	now := time.Now()
	out, err := d.cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace:  awssdk.String(d.metricNamespace),
		MetricName: awssdk.String(gate),
		StartTime:  awssdk.Time(now.Add(-5 * time.Minute)),
		EndTime:    awssdk.Time(now),
		Period:     awssdk.Int32(300),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
	})
	if err != nil {
		return fmt.Errorf("deploy: metric gate %q: cloudwatch error: %w", gate, err)
	}
	if len(out.Datapoints) == 0 {
		// No data — assume healthy.
		return nil
	}
	avg := awssdk.ToFloat64(out.Datapoints[0].Average)
	if avg > d.metricThreshold {
		return fmt.Errorf("deploy: metric gate %q failed: average=%.4f exceeds threshold=%.4f", gate, avg, d.metricThreshold)
	}
	return nil
}

// PromoteCanary promotes the canary by updating the stable service with the
// canary image, routing all traffic back to stable, and destroying the canary.
func (d *ECSDeployDriver) PromoteCanary(ctx context.Context) error {
	if d.canaryServiceName == "" {
		return fmt.Errorf("deploy: promote canary: no canary active (CreateCanary not called)")
	}

	// Retrieve the canary image from its task definition.
	canaryDesc, err := d.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  awssdk.String(d.cluster),
		Services: []string{d.canaryServiceName},
	})
	if err != nil || len(canaryDesc.Services) == 0 {
		return fmt.Errorf("deploy: promote canary: describe canary service: %w", err)
	}
	canaryTDARN := awssdk.ToString(canaryDesc.Services[0].TaskDefinition)

	tdOut, err := d.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: awssdk.String(canaryTDARN),
	})
	if err != nil || tdOut.TaskDefinition == nil || len(tdOut.TaskDefinition.ContainerDefinitions) == 0 {
		return fmt.Errorf("deploy: promote canary: describe task def: %w", err)
	}
	canaryImage := awssdk.ToString(tdOut.TaskDefinition.ContainerDefinitions[0].Image)

	// Update the stable service with the canary image.
	if err := d.Update(ctx, canaryImage); err != nil {
		return fmt.Errorf("deploy: promote canary: update stable service: %w", err)
	}

	// Route all traffic back to stable.
	if _, err := d.elb.ModifyListener(ctx, &elbv2.ModifyListenerInput{
		ListenerArn: awssdk.String(d.listenerARN),
		DefaultActions: []elbtypes.Action{
			{
				Type: elbtypes.ActionTypeEnumForward,
				ForwardConfig: &elbtypes.ForwardActionConfig{
					TargetGroups: []elbtypes.TargetGroupTuple{
						{TargetGroupArn: awssdk.String(d.stableTGARN), Weight: awssdk.Int32(1)},
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("deploy: promote canary: restore stable listener: %w", err)
	}

	return d.destroyCanaryResources(ctx)
}

// DestroyCanary routes all traffic back to stable and removes the canary.
func (d *ECSDeployDriver) DestroyCanary(ctx context.Context) error {
	if d.canaryTGARN == "" && d.canaryServiceName == "" {
		return nil
	}
	if d.stableTGARN != "" && d.listenerARN != "" {
		_, _ = d.elb.ModifyListener(ctx, &elbv2.ModifyListenerInput{
			ListenerArn: awssdk.String(d.listenerARN),
			DefaultActions: []elbtypes.Action{
				{
					Type: elbtypes.ActionTypeEnumForward,
					ForwardConfig: &elbtypes.ForwardActionConfig{
						TargetGroups: []elbtypes.TargetGroupTuple{
							{TargetGroupArn: awssdk.String(d.stableTGARN), Weight: awssdk.Int32(1)},
						},
					},
				},
			},
		})
	}
	return d.destroyCanaryResources(ctx)
}

func (d *ECSDeployDriver) destroyCanaryResources(ctx context.Context) error {
	if d.canaryServiceName != "" {
		_, _ = d.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
			Cluster:      awssdk.String(d.cluster),
			Service:      awssdk.String(d.canaryServiceName),
			DesiredCount: awssdk.Int32(0),
		})
		if _, err := d.ecs.DeleteService(ctx, &ecs.DeleteServiceInput{
			Cluster: awssdk.String(d.cluster),
			Service: awssdk.String(d.canaryServiceName),
			Force:   awssdk.Bool(true),
		}); err != nil {
			return fmt.Errorf("deploy: destroy canary service %q: %w", d.canaryServiceName, err)
		}
		d.canaryServiceName = ""
	}
	if d.canaryTGARN != "" {
		if _, err := d.elb.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
			TargetGroupArn: awssdk.String(d.canaryTGARN),
		}); err != nil {
			return fmt.Errorf("deploy: destroy canary target group: %w", err)
		}
		d.canaryTGARN = ""
	}
	return nil
}

// Compile-time interface checks.
var (
	_ wfmodule.DeployDriver    = (*ECSDeployDriver)(nil)
	_ wfmodule.BlueGreenDriver = (*ECSDeployDriver)(nil)
	_ wfmodule.CanaryDriver    = (*ECSDeployDriver)(nil)
)

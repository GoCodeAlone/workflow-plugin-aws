package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// APIGatewayClient is the subset of API Gateway v2 API used by APIGatewayDriver.
type APIGatewayClient interface {
	CreateApi(ctx context.Context, params *apigw.CreateApiInput, optFns ...func(*apigw.Options)) (*apigw.CreateApiOutput, error)
	GetApi(ctx context.Context, params *apigw.GetApiInput, optFns ...func(*apigw.Options)) (*apigw.GetApiOutput, error)
	GetApis(ctx context.Context, params *apigw.GetApisInput, optFns ...func(*apigw.Options)) (*apigw.GetApisOutput, error)
	UpdateApi(ctx context.Context, params *apigw.UpdateApiInput, optFns ...func(*apigw.Options)) (*apigw.UpdateApiOutput, error)
	DeleteApi(ctx context.Context, params *apigw.DeleteApiInput, optFns ...func(*apigw.Options)) (*apigw.DeleteApiOutput, error)
}

// APIGatewayDriver manages API Gateway v2 APIs (infra.api_gateway).
type APIGatewayDriver struct {
	noSensitiveKeys
	client APIGatewayClient
}

// NewAPIGatewayDriver creates an API Gateway driver from an AWS config.
func NewAPIGatewayDriver(cfg awssdk.Config) *APIGatewayDriver {
	return &APIGatewayDriver{client: apigw.NewFromConfig(cfg)}
}

// NewAPIGatewayDriverWithClient creates an API Gateway driver with a custom client (for tests).
func NewAPIGatewayDriverWithClient(client APIGatewayClient) *APIGatewayDriver {
	return &APIGatewayDriver{client: client}
}

func (d *APIGatewayDriver) ResourceType() string { return "infra.api_gateway" }

func (d *APIGatewayDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	protocol, _ := spec.Config["protocol"].(string)
	if protocol == "" {
		protocol = "HTTP"
	}
	description, _ := spec.Config["description"].(string)

	protocolType := apigwtypes.ProtocolTypeHttp
	if protocol == "WEBSOCKET" {
		protocolType = apigwtypes.ProtocolTypeWebsocket
	}

	in := &apigw.CreateApiInput{
		Name:         awssdk.String(spec.Name),
		ProtocolType: protocolType,
		Description:  awssdk.String(description),
	}

	out, err := d.client.CreateApi(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("apigateway: create %q: %w", spec.Name, err)
	}
	return apigwAPIToOutput(spec.Name, out.ApiId, out.ApiEndpoint, string(protocolType)), nil
}

func (d *APIGatewayDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if ref.ProviderID != "" {
		out, err := d.client.GetApi(ctx, &apigw.GetApiInput{ApiId: awssdk.String(ref.ProviderID)})
		if err != nil {
			return nil, fmt.Errorf("apigateway: get %q: %w", ref.ProviderID, err)
		}
		return apigwAPIToOutput(ref.Name, out.ApiId, out.ApiEndpoint, string(out.ProtocolType)), nil
	}

	// Search by name
	out, err := d.client.GetApis(ctx, &apigw.GetApisInput{})
	if err != nil {
		return nil, fmt.Errorf("apigateway: list APIs: %w", err)
	}
	for _, api := range out.Items {
		if awssdk.ToString(api.Name) == ref.Name {
			return apigwAPIToOutput(ref.Name, api.ApiId, api.ApiEndpoint, string(api.ProtocolType)), nil
		}
	}
	return nil, fmt.Errorf("apigateway: %q not found", ref.Name)
}

func (d *APIGatewayDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return nil, err
	}
	apiID, _ := current.Outputs["api_id"].(string)
	if apiID == "" {
		return nil, fmt.Errorf("apigateway: update %q: no api_id", ref.Name)
	}

	in := &apigw.UpdateApiInput{ApiId: awssdk.String(apiID)}
	if desc, _ := spec.Config["description"].(string); desc != "" {
		in.Description = awssdk.String(desc)
	}

	out, err := d.client.UpdateApi(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("apigateway: update %q: %w", ref.Name, err)
	}
	return apigwAPIToOutput(ref.Name, out.ApiId, out.ApiEndpoint, string(out.ProtocolType)), nil
}

func (d *APIGatewayDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	current, err := d.Read(ctx, ref)
	if err != nil {
		return err
	}
	apiID, _ := current.Outputs["api_id"].(string)
	if apiID == "" {
		return fmt.Errorf("apigateway: delete %q: no api_id", ref.Name)
	}

	_, err = d.client.DeleteApi(ctx, &apigw.DeleteApiInput{ApiId: awssdk.String(apiID)})
	if err != nil {
		return fmt.Errorf("apigateway: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *APIGatewayDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *APIGatewayDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	_, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "api exists"}, nil
}

func (d *APIGatewayDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("apigateway: API Gateway scales automatically")
}

func apigwAPIToOutput(name string, apiID, endpoint *string, protocol string) *interfaces.ResourceOutput {
	outputs := map[string]any{"protocol": protocol}
	if apiID != nil {
		outputs["api_id"] = *apiID
	}
	if endpoint != nil {
		outputs["endpoint"] = *endpoint
	}

	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.api_gateway",
		ProviderID: awssdk.ToString(apiID),
		Outputs:    outputs,
		Status:     "running",
	}
}

var _ interfaces.ResourceDriver = (*APIGatewayDriver)(nil)

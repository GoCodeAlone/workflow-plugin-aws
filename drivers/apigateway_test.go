package drivers_test

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	apigw "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/GoCodeAlone/workflow-plugin-aws/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockAPIGatewayClient struct {
	createOut *apigw.CreateApiOutput
	createErr error
	getOut    *apigw.GetApiOutput
	getErr    error
	getApisOut *apigw.GetApisOutput
	getApisErr error
	updateOut  *apigw.UpdateApiOutput
	updateErr  error
	deleteErr  error
}

func (m *mockAPIGatewayClient) CreateApi(_ context.Context, _ *apigw.CreateApiInput, _ ...func(*apigw.Options)) (*apigw.CreateApiOutput, error) {
	return m.createOut, m.createErr
}
func (m *mockAPIGatewayClient) GetApi(_ context.Context, _ *apigw.GetApiInput, _ ...func(*apigw.Options)) (*apigw.GetApiOutput, error) {
	return m.getOut, m.getErr
}
func (m *mockAPIGatewayClient) GetApis(_ context.Context, _ *apigw.GetApisInput, _ ...func(*apigw.Options)) (*apigw.GetApisOutput, error) {
	return m.getApisOut, m.getApisErr
}
func (m *mockAPIGatewayClient) UpdateApi(_ context.Context, _ *apigw.UpdateApiInput, _ ...func(*apigw.Options)) (*apigw.UpdateApiOutput, error) {
	return m.updateOut, m.updateErr
}
func (m *mockAPIGatewayClient) DeleteApi(_ context.Context, _ *apigw.DeleteApiInput, _ ...func(*apigw.Options)) (*apigw.DeleteApiOutput, error) {
	return &apigw.DeleteApiOutput{}, m.deleteErr
}

func TestAPIGatewayDriver_Create(t *testing.T) {
	mock := &mockAPIGatewayClient{
		createOut: &apigw.CreateApiOutput{
			ApiId:        awssdk.String("abc123"),
			ApiEndpoint:  awssdk.String("https://abc123.execute-api.us-east-1.amazonaws.com"),
			ProtocolType: apigwtypes.ProtocolTypeHttp,
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	out, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"protocol": "HTTP"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if out.Type != "infra.api_gateway" {
		t.Errorf("expected infra.api_gateway, got %s", out.Type)
	}
	if out.ProviderID != "abc123" {
		t.Errorf("expected api_id abc123, got %s", out.ProviderID)
	}
}

func TestAPIGatewayDriver_Read_ByProviderID(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getOut: &apigw.GetApiOutput{
			ApiId:        awssdk.String("abc123"),
			Name:         awssdk.String("my-api"),
			ApiEndpoint:  awssdk.String("https://abc123.execute-api.us-east-1.amazonaws.com"),
			ProtocolType: apigwtypes.ProtocolTypeHttp,
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-api", ProviderID: "abc123"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.Outputs["api_id"] != "abc123" {
		t.Errorf("unexpected api_id: %v", out.Outputs["api_id"])
	}
}

func TestAPIGatewayDriver_Read_ByName(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{
					ApiId:        awssdk.String("abc123"),
					Name:         awssdk.String("my-api"),
					ProtocolType: apigwtypes.ProtocolTypeHttp,
				},
			},
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-api"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if out.ProviderID != "abc123" {
		t.Errorf("expected abc123, got %s", out.ProviderID)
	}
}

func TestAPIGatewayDriver_Delete(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{ApiId: awssdk.String("abc123"), Name: awssdk.String("my-api"), ProtocolType: apigwtypes.ProtocolTypeHttp},
			},
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-api"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestAPIGatewayDriver_Create_Error(t *testing.T) {
	mock := &mockAPIGatewayClient{createErr: fmt.Errorf("API name already exists")}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"protocol": "HTTP"},
	})
	if err == nil {
		t.Fatal("expected error on CreateApi API failure")
	}
}

func TestAPIGatewayDriver_Update_Success(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{ApiId: awssdk.String("abc123"), Name: awssdk.String("my-api"), ProtocolType: apigwtypes.ProtocolTypeHttp},
			},
		},
		updateOut: &apigw.UpdateApiOutput{
			ApiId:        awssdk.String("abc123"),
			ApiEndpoint:  awssdk.String("https://abc123.execute-api.us-east-1.amazonaws.com"),
			ProtocolType: apigwtypes.ProtocolTypeHttp,
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-api"}, interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"description": "Updated API"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
}

func TestAPIGatewayDriver_Update_Error(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{ApiId: awssdk.String("abc123"), Name: awssdk.String("my-api"), ProtocolType: apigwtypes.ProtocolTypeHttp},
			},
		},
		updateErr: fmt.Errorf("invalid API configuration"),
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "my-api"}, interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"description": "bad"},
	})
	if err == nil {
		t.Fatal("expected error on UpdateApi API failure")
	}
}

func TestAPIGatewayDriver_Delete_Error(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{ApiId: awssdk.String("abc123"), Name: awssdk.String("my-api"), ProtocolType: apigwtypes.ProtocolTypeHttp},
			},
		},
		deleteErr: fmt.Errorf("API not found"),
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "my-api"})
	if err == nil {
		t.Fatal("expected error on DeleteApi API failure")
	}
}

func TestAPIGatewayDriver_Diff_NilCurrent(t *testing.T) {
	d := drivers.NewAPIGatewayDriverWithClient(&mockAPIGatewayClient{})
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{Name: "my-api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true for nil current")
	}
}

func TestAPIGatewayDriver_Diff_HasChanges(t *testing.T) {
	d := drivers.NewAPIGatewayDriverWithClient(&mockAPIGatewayClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-api",
		Type:    "infra.api_gateway",
		Outputs: map[string]any{"protocol": "HTTP"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"protocol": "WEBSOCKET"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=true when protocol changes")
	}
}

func TestAPIGatewayDriver_Diff_NoChanges(t *testing.T) {
	d := drivers.NewAPIGatewayDriverWithClient(&mockAPIGatewayClient{})
	current := &interfaces.ResourceOutput{
		Name:    "my-api",
		Type:    "infra.api_gateway",
		Outputs: map[string]any{"protocol": "HTTP"},
	}
	diff, err := d.Diff(context.Background(), interfaces.ResourceSpec{
		Name:   "my-api",
		Config: map[string]any{"protocol": "HTTP"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if diff.NeedsUpdate {
		t.Error("expected NeedsUpdate=false when config unchanged")
	}
}

func TestAPIGatewayDriver_HealthCheck_Healthy(t *testing.T) {
	mock := &mockAPIGatewayClient{
		getApisOut: &apigw.GetApisOutput{
			Items: []apigwtypes.Api{
				{ApiId: awssdk.String("abc123"), Name: awssdk.String("my-api"), ProtocolType: apigwtypes.ProtocolTypeHttp},
			},
		},
	}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "my-api"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy {
		t.Errorf("expected healthy, got: %s", h.Message)
	}
}

func TestAPIGatewayDriver_HealthCheck_Unhealthy(t *testing.T) {
	mock := &mockAPIGatewayClient{getApisErr: fmt.Errorf("API not found")}
	d := drivers.NewAPIGatewayDriverWithClient(mock)
	h, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "missing-api"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Error("expected unhealthy when API not found")
	}
	if h.Message == "" {
		t.Error("expected non-empty message for unhealthy API")
	}
}

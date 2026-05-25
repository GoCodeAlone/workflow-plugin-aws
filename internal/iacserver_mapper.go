package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	awsCollectorModuleName = "observability-collector"
	awsCollectorImage      = "public.ecr.aws/aws-observability/aws-otel-collector:latest"
	awsCollectorType       = "infra.container_service"
)

// MapRequirements maps canonical derived-IaC requirements into AWS-owned
// resource shapes. The v1 mapper emits an ECS service running the AWS OTel
// Collector; app sidecar placement can be supplied explicitly by applications
// with the same satisfies keys when a customer needs per-task sidecars.
func (s *awsIaCServer) MapRequirements(_ context.Context, req *pb.MapRequirementsRequest) (*pb.MapRequirementsResponse, error) {
	if req.GetProvider() != "" && req.GetProvider() != "aws" {
		return nil, status.Errorf(codes.InvalidArgument, "aws mapper cannot satisfy provider %q", req.GetProvider())
	}

	resp := &pb.MapRequirementsResponse{}
	var accepted []*pb.IaCRequirement
	for _, requirement := range req.GetRequirements() {
		switch diag := awsRejectUnsupportedRequirement(req.GetRuntime(), requirement); {
		case diag != nil:
			resp.Rejected = append(resp.Rejected, diag)
		default:
			accepted = append(accepted, requirement)
			resp.AcceptedKeys = append(resp.AcceptedKeys, requirement.GetKey())
		}
	}
	if len(accepted) == 0 {
		return resp, nil
	}

	cfg := awsCollectorModuleConfig(accepted)
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("aws requirement mapper: encode collector config: %w", err)
	}
	resp.Modules = append(resp.Modules, &pb.DerivedModuleSpec{
		Name:       awsCollectorModuleName,
		Type:       awsCollectorType,
		Satisfies:  append([]string(nil), resp.GetAcceptedKeys()...),
		ConfigJson: configJSON,
	})
	resp.Notes = append(resp.Notes, &pb.RequirementNote{
		Key:         strings.Join(resp.GetAcceptedKeys(), ","),
		Message:     "AWS ECS derivation emits a generic AWS OTel Collector service. Use an explicit infra.container_service module with the same satisfies keys when an application requires collector sidecars in its own task definition.",
		Interactive: false,
	})
	return resp, nil
}

func awsRejectUnsupportedRequirement(runtime pb.RequirementRuntime, req *pb.IaCRequirement) *pb.RequirementDiagnostic {
	key := req.GetKey()
	if req.GetKind() != pb.RequirementKind_REQUIREMENT_KIND_OBSERVABILITY {
		return awsRequirementDiagnostic(key, "unsupported_kind", "aws can only derive observability requirements today")
	}
	if hint := req.GetResourceTypeHint(); hint != "" && hint != awsCollectorType {
		return awsRequirementDiagnostic(key, "unsupported_resource_type_hint",
			fmt.Sprintf("aws observability derivation emits %s, not %s", awsCollectorType, hint))
	}
	if runtime != pb.RequirementRuntime_REQUIREMENT_RUNTIME_ECS {
		return awsRequirementDiagnostic(key, "unsupported_runtime", "aws observability derivation currently targets ECS")
	}
	if !awsRequirementAllowsRuntime(req, runtime) {
		return awsRequirementDiagnostic(key, "unsupported_runtime", "requirement does not allow ECS")
	}
	if !awsRequirementAllowsDeploymentMode(req) {
		return awsRequirementDiagnostic(key, "unsupported_deployment_mode",
			"aws ECS maps sidecar intent to an explicit or sibling collector service; daemonset mode is not valid for ECS")
	}
	return nil
}

func awsRequirementAllowsRuntime(req *pb.IaCRequirement, runtime pb.RequirementRuntime) bool {
	if len(req.GetRuntimes()) == 0 {
		return true
	}
	for _, candidate := range req.GetRuntimes() {
		if candidate == runtime {
			return true
		}
	}
	return false
}

func awsRequirementAllowsDeploymentMode(req *pb.IaCRequirement) bool {
	modes := req.GetDeploymentModes()
	if len(modes) == 0 {
		return true
	}
	for _, mode := range modes {
		switch mode {
		case pb.DeploymentMode_DEPLOYMENT_MODE_SIDECAR,
			pb.DeploymentMode_DEPLOYMENT_MODE_SIBLING_SERVICE,
			pb.DeploymentMode_DEPLOYMENT_MODE_MANAGED:
			return true
		}
	}
	return false
}

func awsRequirementDiagnostic(key, code, message string) *pb.RequirementDiagnostic {
	return &pb.RequirementDiagnostic{Key: key, Code: code, Message: message}
}

func awsCollectorModuleConfig(reqs []*pb.IaCRequirement) map[string]any {
	signals := awsRequestedSignals(reqs)
	backends := awsRequestedBackends(reqs)
	collectorConfig := awsBuildCollectorConfig(signals, backends)

	envVars := map[string]any{
		"AOT_CONFIG_CONTENT": collectorConfig,
	}
	secretVars := make(map[string]any)
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_OTEL) {
		envVars["OTEL_EXPORTER_OTLP_ENDPOINT"] = "${vars.otel_exporter_otlp_endpoint}"
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_DATADOG) {
		envVars["DD_SITE"] = "${vars.datadog_site}"
		secretVars["DD_API_KEY"] = "${secrets.datadog_api_key_arn}"
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_LOKI) {
		envVars["LOKI_ENDPOINT"] = "${vars.loki_endpoint}"
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_GRAFANA) {
		envVars["GRAFANA_OTLP_ENDPOINT"] = "${vars.grafana_otlp_endpoint}"
		secretVars["GRAFANA_OTLP_HEADERS"] = "${secrets.grafana_otlp_headers_arn}"
	}

	cfg := map[string]any{
		"image":           awsCollectorImage,
		"command":         []any{"--config=env:AOT_CONFIG_CONTENT"},
		"cpu":             "256",
		"memory":          "512",
		"replicas":        1,
		"ports":           awsCollectorPorts(backends),
		"env_vars":        envVars,
		"env_vars_secret": secretVars,
	}
	return cfg
}

func awsRequestedSignals(reqs []*pb.IaCRequirement) map[pb.TelemetrySignal]bool {
	out := make(map[pb.TelemetrySignal]bool)
	for _, req := range reqs {
		for _, signal := range req.GetTelemetrySignals() {
			if signal != pb.TelemetrySignal_TELEMETRY_SIGNAL_UNSPECIFIED {
				out[signal] = true
			}
		}
	}
	if len(out) == 0 {
		out[pb.TelemetrySignal_TELEMETRY_SIGNAL_TRACES] = true
		out[pb.TelemetrySignal_TELEMETRY_SIGNAL_METRICS] = true
		out[pb.TelemetrySignal_TELEMETRY_SIGNAL_LOGS] = true
	}
	return out
}

func awsRequestedBackends(reqs []*pb.IaCRequirement) map[pb.ObservabilityBackend]bool {
	out := make(map[pb.ObservabilityBackend]bool)
	for _, req := range reqs {
		for _, backend := range req.GetObservabilityBackends() {
			if backend != pb.ObservabilityBackend_OBSERVABILITY_BACKEND_UNSPECIFIED {
				out[backend] = true
			}
		}
	}
	if len(out) == 0 {
		out[pb.ObservabilityBackend_OBSERVABILITY_BACKEND_OTEL] = true
	}
	return out
}

func awsCollectorPorts(backends map[pb.ObservabilityBackend]bool) []any {
	ports := []any{
		map[string]any{"port": 4317, "protocol": "tcp"},
		map[string]any{"port": 4318, "protocol": "tcp"},
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_PROMETHEUS) {
		ports = append(ports, map[string]any{"port": 9464, "protocol": "tcp"})
	}
	return ports
}

func awsBuildCollectorConfig(signals map[pb.TelemetrySignal]bool, backends map[pb.ObservabilityBackend]bool) string {
	var b strings.Builder
	b.WriteString("receivers:\n")
	b.WriteString("  otlp:\n")
	b.WriteString("    protocols:\n")
	b.WriteString("      grpc:\n")
	b.WriteString("        endpoint: 0.0.0.0:4317\n")
	b.WriteString("      http:\n")
	b.WriteString("        endpoint: 0.0.0.0:4318\n")
	b.WriteString("processors:\n")
	b.WriteString("  batch: {}\n")
	b.WriteString("exporters:\n")
	awsWriteExporters(&b, backends)
	b.WriteString("service:\n")
	b.WriteString("  pipelines:\n")
	if signals[pb.TelemetrySignal_TELEMETRY_SIGNAL_TRACES] {
		awsWritePipeline(&b, "traces", awsExportersForSignal(pb.TelemetrySignal_TELEMETRY_SIGNAL_TRACES, backends))
	}
	if signals[pb.TelemetrySignal_TELEMETRY_SIGNAL_METRICS] {
		awsWritePipeline(&b, "metrics", awsExportersForSignal(pb.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, backends))
	}
	if signals[pb.TelemetrySignal_TELEMETRY_SIGNAL_LOGS] {
		awsWritePipeline(&b, "logs", awsExportersForSignal(pb.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, backends))
	}
	return b.String()
}

func awsWriteExporters(b *strings.Builder, backends map[pb.ObservabilityBackend]bool) {
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_OTEL) {
		b.WriteString("  otlp:\n")
		b.WriteString("    endpoint: ${env:OTEL_EXPORTER_OTLP_ENDPOINT}\n")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_DATADOG) {
		b.WriteString("  datadog:\n")
		b.WriteString("    api:\n")
		b.WriteString("      key: ${env:DD_API_KEY}\n")
		b.WriteString("      site: ${env:DD_SITE}\n")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_PROMETHEUS) {
		b.WriteString("  prometheus:\n")
		b.WriteString("    endpoint: 0.0.0.0:9464\n")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_LOKI) {
		b.WriteString("  loki:\n")
		b.WriteString("    endpoint: ${env:LOKI_ENDPOINT}\n")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_GRAFANA) {
		b.WriteString("  otlp/grafana_otlp:\n")
		b.WriteString("    endpoint: ${env:GRAFANA_OTLP_ENDPOINT}\n")
		b.WriteString("    headers:\n")
		b.WriteString("      authorization: ${env:GRAFANA_OTLP_HEADERS}\n")
	}
}

func awsWritePipeline(b *strings.Builder, name string, exporters []string) {
	if len(exporters) == 0 {
		return
	}
	b.WriteString("    ")
	b.WriteString(name)
	b.WriteString(":\n")
	b.WriteString("      receivers: [otlp]\n")
	b.WriteString("      processors: [batch]\n")
	b.WriteString("      exporters: [")
	b.WriteString(strings.Join(exporters, ", "))
	b.WriteString("]\n")
}

func awsExportersForSignal(signal pb.TelemetrySignal, backends map[pb.ObservabilityBackend]bool) []string {
	var exporters []string
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_OTEL) {
		exporters = append(exporters, "otlp")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_DATADOG) {
		exporters = append(exporters, "datadog")
	}
	if signal == pb.TelemetrySignal_TELEMETRY_SIGNAL_METRICS &&
		awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_PROMETHEUS) {
		exporters = append(exporters, "prometheus")
	}
	if signal == pb.TelemetrySignal_TELEMETRY_SIGNAL_LOGS &&
		awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_LOKI) {
		exporters = append(exporters, "loki")
	}
	if awsHasBackend(backends, pb.ObservabilityBackend_OBSERVABILITY_BACKEND_GRAFANA) {
		exporters = append(exporters, "otlp/grafana_otlp")
	}
	sort.Strings(exporters)
	return exporters
}

func awsHasBackend(backends map[pb.ObservabilityBackend]bool, backend pb.ObservabilityBackend) bool {
	return backends[backend]
}

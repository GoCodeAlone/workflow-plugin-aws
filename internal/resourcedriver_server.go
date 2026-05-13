// Package internal — typed pb.ResourceDriverServer implementation.
//
// Extends *awsIaCServer (declared in iacserver.go) with the 9 RPC methods
// required by pb.ResourceDriverServer. Routing dispatches per-resource-type
// CRUD by looking up the driver via *provider.AWSProvider.ResourceDriver(type).
//
// Once *awsIaCServer satisfies pb.ResourceDriverServer at the Go type level,
// sdk.RegisterAllIaCProviderServices auto-registers it — no manual call needed.
package internal

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/GoCodeAlone/workflow/interfaces"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

// resolveResourceDriver looks up the per-type driver registered on the
// underlying *provider.AWSProvider. Returns a typed gRPC error with
// codes.NotFound when the resource_type is not registered.
func (s *awsIaCServer) resolveResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	if resourceType == "" {
		return nil, status.Error(codes.InvalidArgument, "aws ResourceDriver: resource_type is required")
	}
	d, err := s.provider.ResourceDriver(resourceType)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "aws ResourceDriver: %v", err)
	}
	return d, nil
}

func (s *awsIaCServer) Create(ctx context.Context, req *pb.ResourceCreateRequest) (*pb.ResourceCreateResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	spec, err := specFromPB(req.GetSpec())
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Create: decode spec: %w", req.GetResourceType(), err)
	}
	out, err := driver.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPB(out)
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Create: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceCreateResponse{Output: pbOut}, nil
}

func (s *awsIaCServer) Read(ctx context.Context, req *pb.ResourceReadRequest) (*pb.ResourceReadResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	out, err := driver.Read(ctx, refFromPB(req.GetRef()))
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPB(out)
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Read: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceReadResponse{Output: pbOut}, nil
}

func (s *awsIaCServer) Update(ctx context.Context, req *pb.ResourceUpdateRequest) (*pb.ResourceUpdateResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	spec, err := specFromPB(req.GetSpec())
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Update: decode spec: %w", req.GetResourceType(), err)
	}
	out, err := driver.Update(ctx, refFromPB(req.GetRef()), spec)
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPB(out)
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Update: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceUpdateResponse{Output: pbOut}, nil
}

func (s *awsIaCServer) Delete(ctx context.Context, req *pb.ResourceDeleteRequest) (*pb.ResourceDeleteResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	if err := driver.Delete(ctx, refFromPB(req.GetRef())); err != nil {
		return nil, err
	}
	return &pb.ResourceDeleteResponse{}, nil
}

func (s *awsIaCServer) Diff(ctx context.Context, req *pb.ResourceDiffRequest) (*pb.ResourceDiffResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	desired, err := specFromPB(req.GetDesired())
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Diff: decode desired: %w", req.GetResourceType(), err)
	}
	current, err := outputFromPB(req.GetCurrent())
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Diff: decode current: %w", req.GetResourceType(), err)
	}
	result, err := driver.Diff(ctx, desired, current)
	if err != nil {
		return nil, err
	}
	pbResult, err := diffResultToPB(result)
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Diff: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceDiffResponse{Result: pbResult}, nil
}

func (s *awsIaCServer) Scale(ctx context.Context, req *pb.ResourceScaleRequest) (*pb.ResourceScaleResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	out, err := driver.Scale(ctx, refFromPB(req.GetRef()), int(req.GetReplicas()))
	if err != nil {
		return nil, err
	}
	pbOut, err := outputToPB(out)
	if err != nil {
		return nil, fmt.Errorf("aws ResourceDriver(%s).Scale: encode response: %w", req.GetResourceType(), err)
	}
	return &pb.ResourceScaleResponse{Output: pbOut}, nil
}

func (s *awsIaCServer) HealthCheck(ctx context.Context, req *pb.ResourceHealthCheckRequest) (*pb.ResourceHealthCheckResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	result, err := driver.HealthCheck(ctx, refFromPB(req.GetRef()))
	if err != nil {
		return nil, err
	}
	return &pb.ResourceHealthCheckResponse{Result: healthResultToPB(result)}, nil
}

func (s *awsIaCServer) SensitiveKeys(_ context.Context, req *pb.SensitiveKeysRequest) (*pb.SensitiveKeysResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	keys := driver.SensitiveKeys()
	return &pb.SensitiveKeysResponse{Keys: append([]string(nil), keys...)}, nil
}

func (s *awsIaCServer) Troubleshoot(ctx context.Context, req *pb.TroubleshootRequest) (*pb.TroubleshootResponse, error) {
	driver, err := s.resolveResourceDriver(req.GetResourceType())
	if err != nil {
		return nil, err
	}
	tr, ok := driver.(interfaces.Troubleshooter)
	if !ok {
		return nil, status.Errorf(codes.Unimplemented,
			"aws ResourceDriver(%s).Troubleshoot: driver does not implement interfaces.Troubleshooter",
			req.GetResourceType())
	}
	diags, err := tr.Troubleshoot(ctx, refFromPB(req.GetRef()), req.GetFailureMsg())
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Diagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, &pb.Diagnostic{
			Id:     d.ID,
			Phase:  d.Phase,
			Cause:  d.Cause,
			At:     timeToPB(d.At),
			Detail: d.Detail,
		})
	}
	return &pb.TroubleshootResponse{Diagnostics: out}, nil
}

// ── Marshalling helpers specific to ResourceDriver ──────────────────────────

func diffResultToPB(r *interfaces.DiffResult) (*pb.DiffResult, error) {
	if r == nil {
		return nil, nil
	}
	pbChanges, err := changesToPB(r.Changes)
	if err != nil {
		return nil, err
	}
	return &pb.DiffResult{
		NeedsUpdate:  r.NeedsUpdate,
		NeedsReplace: r.NeedsReplace,
		Changes:      pbChanges,
	}, nil
}

func healthResultToPB(r *interfaces.HealthResult) *pb.HealthResult {
	if r == nil {
		return nil
	}
	return &pb.HealthResult{Healthy: r.Healthy, Message: r.Message}
}

func outputFromPB(o *pb.ResourceOutput) (*interfaces.ResourceOutput, error) {
	if o == nil {
		return nil, nil
	}
	outputs, err := unmarshalJSONMap(o.GetOutputsJson())
	if err != nil {
		return nil, err
	}
	sensitive := make(map[string]bool, len(o.GetSensitive()))
	for k, v := range o.GetSensitive() {
		sensitive[k] = v
	}
	return &interfaces.ResourceOutput{
		Name:       o.GetName(),
		Type:       o.GetType(),
		ProviderID: o.GetProviderId(),
		Outputs:    outputs,
		Sensitive:  sensitive,
		Status:     o.GetStatus(),
	}, nil
}

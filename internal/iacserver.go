// Package internal — typed pb.IaCProvider*Server implementation.
//
// awsIaCServer is the SERVER side of the typed IaC contract. It satisfies
// pb.IaCProviderRequiredServer plus the optional pb.IaCProviderDriftDetectorServer
// interface by delegating each typed RPC to the matching method on the
// underlying *provider.AWSProvider.
//
// The remaining optional services (Enumerator, CredentialRevoker, MigrationRepairer,
// Validator, DriftConfigDetector) are present as Unimplemented*Server embeds only
// (forward-compat; not auto-registered by sdk.RegisterAllIaCProviderServices).
//
// Hard invariants (strict-contracts force-cutover):
//   - NO structpb.Struct, NO Any.UnmarshalTo on the wire — provider-specific
//     config / outputs cross as JSON bytes (config_json, outputs_json).
//   - REQUIRED service methods MUST be implemented; the SDK type-assert in
//     sdk.RegisterAllIaCProviderServices fails at plugin startup otherwise.
package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-aws/provider"
	"github.com/GoCodeAlone/workflow/interfaces"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Version is set at build time via -ldflags
// "-X github.com/GoCodeAlone/workflow-plugin-aws/internal.Version=X.Y.Z".
// Consumed by main.go via sdk.ResolveBuildVersion to drive the BuildVersion
// field of sdk.IaCServeOptions (workflow#758 Layer 3: tag-only plugin version).
var Version = "0.0.0"

// awsIaCServer wraps *provider.AWSProvider and exposes the typed
// pb.IaCProvider*Server + ResourceDriverServer surface. The Unimplemented*Server
// embeds satisfy the gRPC forward-compat contract and let the SDK type-assert
// succeed. awsIaCServer overrides all Required methods and the DriftDetector methods.
type awsIaCServer struct {
	pb.UnimplementedIaCProviderRequiredServer
	pb.UnimplementedIaCProviderEnumeratorServer
	pb.UnimplementedIaCProviderDriftDetectorServer
	pb.UnimplementedIaCProviderCredentialRevokerServer
	pb.UnimplementedIaCProviderMigrationRepairerServer
	pb.UnimplementedIaCProviderValidatorServer
	pb.UnimplementedIaCProviderDriftConfigDetectorServer
	pb.UnimplementedIaCProviderRequirementMapperServer
	pb.UnimplementedResourceDriverServer
	pb.UnimplementedIaCStateBackendServer

	provider *provider.AWSProvider

	// stateBackend serves the typed pb.IaCStateBackendServer surface
	// (s3 backend). Per decisions/0035, this one type carries both the
	// IaC-provider and the IaC-state-backend concerns. The backing store is
	// constructed lazily via the Configure RPC — see internal/statebackend_server.go.
	stateBackend stateBackend
}

// newAWSIaCServer constructs a typed-IaC server backed by the given
// *provider.AWSProvider. The provider is NOT initialized here; Initialize is
// the first typed RPC the host sends after the gRPC dial completes.
func newAWSIaCServer(p *provider.AWSProvider) *awsIaCServer {
	return &awsIaCServer{provider: p}
}

// NewIaCServer is the package entrypoint used by cmd/workflow-plugin-aws/main.go.
// It constructs a fresh *provider.AWSProvider and wraps it in the typed
// pb.IaCProvider* server surface. The returned value is suitable to pass to
// sdk.ServeIaCPlugin; the SDK auto-registers every typed gRPC service the
// server satisfies via Go type-assertion at plugin startup.
func NewIaCServer() *awsIaCServer {
	return newAWSIaCServer(provider.NewAWSProviderConcrete())
}

// Compile-time guards: every typed server interface this AWS plugin advertises
// MUST be satisfied. A signature drift on any of these will fail the build at
// this file rather than at first RPC dispatch.
var (
	_ pb.IaCProviderRequiredServer = (*awsIaCServer)(nil)
	// IaCProviderDriftDetectorServer requires BOTH DetectDrift AND DetectDriftWithSpecs.
	// Both are implemented below: DetectDrift is the real check; DetectDriftWithSpecs
	// delegates to DetectDrift (existence-only behavior; ignores the specs map).
	_ pb.IaCProviderDriftDetectorServer     = (*awsIaCServer)(nil)
	_ pb.ResourceDriverServer               = (*awsIaCServer)(nil)
	_ pb.IaCProviderRequirementMapperServer = (*awsIaCServer)(nil)
	// awsIaCServer also SERVES the typed IaC state-backend contract (s3
	// backend). The SDK serve hook auto-registers this via type-assertion at
	// plugin startup — see cmd/workflow-plugin-aws/main.go.
	_ pb.IaCStateBackendServer = (*awsIaCServer)(nil)
)

// ── Required service methods ────────────────────────────────────────────────

func (s *awsIaCServer) Initialize(ctx context.Context, req *pb.InitializeRequest) (*pb.InitializeResponse, error) {
	cfg, err := unmarshalJSONMap(req.GetConfigJson())
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: parse Initialize config_json: %w", err)
	}
	if err := s.provider.Initialize(ctx, cfg); err != nil {
		return nil, err
	}
	return &pb.InitializeResponse{}, nil
}

func (s *awsIaCServer) Name(_ context.Context, _ *pb.NameRequest) (*pb.NameResponse, error) {
	return &pb.NameResponse{Name: s.provider.Name()}, nil
}

func (s *awsIaCServer) Version(_ context.Context, _ *pb.VersionRequest) (*pb.VersionResponse, error) {
	return &pb.VersionResponse{Version: s.provider.Version()}, nil
}

func (s *awsIaCServer) Capabilities(_ context.Context, _ *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	caps := s.provider.Capabilities()
	out := make([]*pb.IaCCapabilityDeclaration, 0, len(caps))
	for _, c := range caps {
		tier := c.Tier
		if tier < math.MinInt32 {
			tier = math.MinInt32
		} else if tier > math.MaxInt32 {
			tier = math.MaxInt32
		}
		out = append(out, &pb.IaCCapabilityDeclaration{
			ResourceType: c.ResourceType,
			Tier:         int32(tier), //nolint:gosec // G115: clamped above
			Operations:   append([]string(nil), c.Operations...),
		})
	}
	return &pb.CapabilitiesResponse{Capabilities: out, ComputePlanVersion: "v2"}, nil
}

func (s *awsIaCServer) Plan(ctx context.Context, req *pb.PlanRequest) (*pb.PlanResponse, error) {
	desired, err := specsFromPB(req.GetDesired())
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: decode Plan desired: %w", err)
	}
	current, err := statesFromPB(req.GetCurrent())
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: decode Plan current: %w", err)
	}
	plan, err := s.provider.Plan(ctx, desired, current)
	if err != nil {
		return nil, err
	}
	pbPlan, err := planToPB(plan)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode Plan response: %w", err)
	}
	return &pb.PlanResponse{Plan: pbPlan}, nil
}

func (s *awsIaCServer) Destroy(ctx context.Context, req *pb.DestroyRequest) (*pb.DestroyResponse, error) {
	refs := refsFromPB(req.GetRefs())
	result, err := s.provider.Destroy(ctx, refs)
	if err != nil {
		return nil, err
	}
	return &pb.DestroyResponse{Result: destroyResultToPB(result)}, nil
}

func (s *awsIaCServer) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	refs := refsFromPB(req.GetRefs())
	statuses, err := s.provider.Status(ctx, refs)
	if err != nil {
		return nil, err
	}
	pbStatuses, err := statusesToPB(statuses)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode Status response: %w", err)
	}
	return &pb.StatusResponse{Statuses: pbStatuses}, nil
}

func (s *awsIaCServer) Import(ctx context.Context, req *pb.ImportRequest) (*pb.ImportResponse, error) {
	state, err := s.provider.Import(ctx, req.GetProviderId(), req.GetResourceType())
	if err != nil {
		return nil, err
	}
	pbState, err := stateToPB(state)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode Import response: %w", err)
	}
	return &pb.ImportResponse{State: pbState}, nil
}

func (s *awsIaCServer) ResolveSizing(_ context.Context, req *pb.ResolveSizingRequest) (*pb.ResolveSizingResponse, error) {
	sizing, err := s.provider.ResolveSizing(
		req.GetResourceType(),
		interfaces.Size(req.GetSize()),
		hintsFromPB(req.GetHints()),
	)
	if err != nil {
		return nil, err
	}
	pbSizing, err := sizingToPB(sizing)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode ResolveSizing response: %w", err)
	}
	return &pb.ResolveSizingResponse{Sizing: pbSizing}, nil
}

func (s *awsIaCServer) BootstrapStateBackend(ctx context.Context, req *pb.BootstrapStateBackendRequest) (*pb.BootstrapStateBackendResponse, error) {
	cfg, err := unmarshalJSONMap(req.GetConfigJson())
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: parse BootstrapStateBackend config_json: %w", err)
	}
	result, err := s.provider.BootstrapStateBackend(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pb.BootstrapStateBackendResponse{Result: bootstrapResultToPB(result)}, nil
}

// ── Optional: DriftDetector ────────────────────────────────────────────────

func (s *awsIaCServer) DetectDrift(ctx context.Context, req *pb.DetectDriftRequest) (*pb.DetectDriftResponse, error) {
	refs := refsFromPB(req.GetRefs())
	drifts, err := s.provider.DetectDrift(ctx, refs)
	if err != nil {
		return nil, err
	}
	pbDrifts, err := driftsToPB(drifts)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode DetectDrift response: %w", err)
	}
	return &pb.DetectDriftResponse{Drifts: pbDrifts}, nil
}

// DetectDriftWithSpecs satisfies pb.IaCProviderDriftDetectorServer.
// AWSProvider only implements existence-check drift detection; this method
// delegates to DetectDrift and ignores the specs map (consistent with
// existence-only behavior). Both methods are required for IaCProviderDriftDetector
// to register cleanly via sdk.RegisterAllIaCProviderServices.
func (s *awsIaCServer) DetectDriftWithSpecs(ctx context.Context, req *pb.DetectDriftWithSpecsRequest) (*pb.DetectDriftWithSpecsResponse, error) {
	refs := refsFromPB(req.GetRefs())
	drifts, err := s.provider.DetectDrift(ctx, refs)
	if err != nil {
		return nil, err
	}
	pbDrifts, err := driftsToPB(drifts)
	if err != nil {
		return nil, fmt.Errorf("aws iacserver: encode DetectDriftWithSpecs response: %w", err)
	}
	return &pb.DetectDriftWithSpecsResponse{Drifts: pbDrifts}, nil
}

// ── Marshalling helpers (pb ↔ Go) ───────────────────────────────────────────
//
// These mirror the inverse-direction helpers in cmd/wfctl/iac_typed_adapter.go
// (workflow). Pattern copied from workflow-plugin-digitalocean v1.0.1 iacserver.go.

func unmarshalJSONMap(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalJSONMap(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func marshalJSONAny(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func unmarshalJSONAny(b []byte) (any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func refToPB(r interfaces.ResourceRef) *pb.ResourceRef {
	return &pb.ResourceRef{Name: r.Name, Type: r.Type, ProviderId: r.ProviderID}
}

func refFromPB(r *pb.ResourceRef) interfaces.ResourceRef {
	if r == nil {
		return interfaces.ResourceRef{}
	}
	return interfaces.ResourceRef{Name: r.GetName(), Type: r.GetType(), ProviderID: r.GetProviderId()}
}

func refsToPB(refs []interfaces.ResourceRef) []*pb.ResourceRef {
	out := make([]*pb.ResourceRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, refToPB(r))
	}
	return out
}

func refsFromPB(refs []*pb.ResourceRef) []interfaces.ResourceRef {
	out := make([]interfaces.ResourceRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, refFromPB(r))
	}
	return out
}

func hintsToPB(h *interfaces.ResourceHints) *pb.ResourceHints {
	if h == nil {
		return nil
	}
	return &pb.ResourceHints{Cpu: h.CPU, Memory: h.Memory, Storage: h.Storage}
}

func hintsFromPB(h *pb.ResourceHints) *interfaces.ResourceHints {
	if h == nil {
		return nil
	}
	return &interfaces.ResourceHints{CPU: h.GetCpu(), Memory: h.GetMemory(), Storage: h.GetStorage()}
}

func specToPB(s interfaces.ResourceSpec) (*pb.ResourceSpec, error) {
	cfgJSON, err := marshalJSONMap(s.Config)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceSpec{
		Name:       s.Name,
		Type:       s.Type,
		ConfigJson: cfgJSON,
		Size:       string(s.Size),
		Hints:      hintsToPB(s.Hints),
		DependsOn:  append([]string(nil), s.DependsOn...),
	}, nil
}

func specFromPB(s *pb.ResourceSpec) (interfaces.ResourceSpec, error) {
	if s == nil {
		return interfaces.ResourceSpec{}, nil
	}
	cfg, err := unmarshalJSONMap(s.GetConfigJson())
	if err != nil {
		return interfaces.ResourceSpec{}, err
	}
	return interfaces.ResourceSpec{
		Name:      s.GetName(),
		Type:      s.GetType(),
		Config:    cfg,
		Size:      interfaces.Size(s.GetSize()),
		Hints:     hintsFromPB(s.GetHints()),
		DependsOn: append([]string(nil), s.GetDependsOn()...),
	}, nil
}

func specsFromPB(specs []*pb.ResourceSpec) ([]interfaces.ResourceSpec, error) {
	out := make([]interfaces.ResourceSpec, 0, len(specs))
	for _, s := range specs {
		gs, err := specFromPB(s)
		if err != nil {
			return nil, err
		}
		out = append(out, gs)
	}
	return out, nil
}

func stateToPB(st *interfaces.ResourceState) (*pb.ResourceState, error) {
	if st == nil {
		return nil, nil
	}
	appliedJSON, err := marshalJSONMap(st.AppliedConfig)
	if err != nil {
		return nil, err
	}
	outputsJSON, err := marshalJSONMap(st.Outputs)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceState{
		Id:                  st.ID,
		Name:                st.Name,
		Type:                st.Type,
		Provider:            st.Provider,
		ProviderRef:         st.ProviderRef,
		ProviderId:          st.ProviderID,
		ConfigHash:          st.ConfigHash,
		AppliedConfigJson:   appliedJSON,
		AppliedConfigSource: st.AppliedConfigSource,
		OutputsJson:         outputsJSON,
		Dependencies:        append([]string(nil), st.Dependencies...),
		CreatedAt:           timeToPB(st.CreatedAt),
		UpdatedAt:           timeToPB(st.UpdatedAt),
		LastDriftCheck:      timeToPB(st.LastDriftCheck),
	}, nil
}

func stateFromPB(s *pb.ResourceState) (*interfaces.ResourceState, error) {
	if s == nil {
		return nil, nil
	}
	applied, err := unmarshalJSONMap(s.GetAppliedConfigJson())
	if err != nil {
		return nil, err
	}
	outputs, err := unmarshalJSONMap(s.GetOutputsJson())
	if err != nil {
		return nil, err
	}
	return &interfaces.ResourceState{
		ID:                  s.GetId(),
		Name:                s.GetName(),
		Type:                s.GetType(),
		Provider:            s.GetProvider(),
		ProviderRef:         s.GetProviderRef(),
		ProviderID:          s.GetProviderId(),
		ConfigHash:          s.GetConfigHash(),
		AppliedConfig:       applied,
		AppliedConfigSource: s.GetAppliedConfigSource(),
		Outputs:             outputs,
		Dependencies:        append([]string(nil), s.GetDependencies()...),
		CreatedAt:           timeFromPB(s.GetCreatedAt()),
		UpdatedAt:           timeFromPB(s.GetUpdatedAt()),
		LastDriftCheck:      timeFromPB(s.GetLastDriftCheck()),
	}, nil
}

func statesFromPB(states []*pb.ResourceState) ([]interfaces.ResourceState, error) {
	out := make([]interfaces.ResourceState, 0, len(states))
	for _, s := range states {
		gs, err := stateFromPB(s)
		if err != nil {
			return nil, err
		}
		if gs != nil {
			out = append(out, *gs)
		}
	}
	return out, nil
}

func outputToPB(o *interfaces.ResourceOutput) (*pb.ResourceOutput, error) {
	if o == nil {
		return nil, nil
	}
	outputsJSON, err := marshalJSONMap(o.Outputs)
	if err != nil {
		return nil, err
	}
	sensitive := make(map[string]bool, len(o.Sensitive))
	for k, v := range o.Sensitive {
		sensitive[k] = v
	}
	return &pb.ResourceOutput{
		Name:        o.Name,
		Type:        o.Type,
		ProviderId:  o.ProviderID,
		OutputsJson: outputsJSON,
		Sensitive:   sensitive,
		Status:      o.Status,
	}, nil
}

func statusesToPB(ss []interfaces.ResourceStatus) ([]*pb.ResourceStatus, error) {
	out := make([]*pb.ResourceStatus, 0, len(ss))
	for i := range ss {
		o, err := marshalJSONMap(ss[i].Outputs)
		if err != nil {
			return nil, err
		}
		out = append(out, &pb.ResourceStatus{
			Name:        ss[i].Name,
			Type:        ss[i].Type,
			ProviderId:  ss[i].ProviderID,
			Status:      ss[i].Status,
			OutputsJson: o,
		})
	}
	return out, nil
}

func driftClassToPB(c interfaces.DriftClass) pb.DriftClass {
	switch c {
	case interfaces.DriftClassInSync:
		return pb.DriftClass_DRIFT_CLASS_IN_SYNC
	case interfaces.DriftClassGhost:
		return pb.DriftClass_DRIFT_CLASS_GHOST
	case interfaces.DriftClassConfig:
		return pb.DriftClass_DRIFT_CLASS_CONFIG
	default:
		return pb.DriftClass_DRIFT_CLASS_UNKNOWN
	}
}

func driftsToPB(drifts []interfaces.DriftResult) ([]*pb.DriftResult, error) {
	out := make([]*pb.DriftResult, 0, len(drifts))
	for _, d := range drifts {
		expectedJSON, err := marshalJSONMap(d.Expected)
		if err != nil {
			return nil, err
		}
		actualJSON, err := marshalJSONMap(d.Actual)
		if err != nil {
			return nil, err
		}
		out = append(out, &pb.DriftResult{
			Name:         d.Name,
			Type:         d.Type,
			Drifted:      d.Drifted,
			Class:        driftClassToPB(d.Class),
			ExpectedJson: expectedJSON,
			ActualJson:   actualJSON,
			Fields:       append([]string(nil), d.Fields...),
		})
	}
	return out, nil
}

func planActionToPB(a interfaces.PlanAction) (*pb.PlanAction, error) {
	pbSpec, err := specToPB(a.Resource)
	if err != nil {
		return nil, err
	}
	var pbCurrent *pb.ResourceState
	if a.Current != nil {
		pbCurrent, err = stateToPB(a.Current)
		if err != nil {
			return nil, err
		}
	}
	pbChanges, err := changesToPB(a.Changes)
	if err != nil {
		return nil, err
	}
	return &pb.PlanAction{
		Action:             a.Action,
		Resource:           pbSpec,
		Current:            pbCurrent,
		Changes:            pbChanges,
		ResolvedConfigHash: a.ResolvedConfigHash,
	}, nil
}

func planActionFromPB(a *pb.PlanAction) (interfaces.PlanAction, error) {
	if a == nil {
		return interfaces.PlanAction{}, nil
	}
	spec, err := specFromPB(a.GetResource())
	if err != nil {
		return interfaces.PlanAction{}, err
	}
	var current *interfaces.ResourceState
	if a.GetCurrent() != nil {
		current, err = stateFromPB(a.GetCurrent())
		if err != nil {
			return interfaces.PlanAction{}, err
		}
	}
	changes, err := changesFromPB(a.GetChanges())
	if err != nil {
		return interfaces.PlanAction{}, err
	}
	return interfaces.PlanAction{
		Action:             a.GetAction(),
		Resource:           spec,
		Current:            current,
		Changes:            changes,
		ResolvedConfigHash: a.GetResolvedConfigHash(),
	}, nil
}

func changesToPB(changes []interfaces.FieldChange) ([]*pb.FieldChange, error) {
	out := make([]*pb.FieldChange, 0, len(changes))
	for _, c := range changes {
		oldJSON, err := marshalJSONAny(c.Old)
		if err != nil {
			return nil, err
		}
		newJSON, err := marshalJSONAny(c.New)
		if err != nil {
			return nil, err
		}
		out = append(out, &pb.FieldChange{
			Path:     c.Path,
			OldJson:  oldJSON,
			NewJson:  newJSON,
			ForceNew: c.ForceNew,
		})
	}
	return out, nil
}

func changesFromPB(changes []*pb.FieldChange) ([]interfaces.FieldChange, error) {
	out := make([]interfaces.FieldChange, 0, len(changes))
	for _, c := range changes {
		oldVal, err := unmarshalJSONAny(c.GetOldJson())
		if err != nil {
			return nil, err
		}
		newVal, err := unmarshalJSONAny(c.GetNewJson())
		if err != nil {
			return nil, err
		}
		out = append(out, interfaces.FieldChange{
			Path:     c.GetPath(),
			Old:      oldVal,
			New:      newVal,
			ForceNew: c.GetForceNew(),
		})
	}
	return out, nil
}

func planToPB(p *interfaces.IaCPlan) (*pb.IaCPlan, error) {
	if p == nil {
		return nil, nil
	}
	pbActions := make([]*pb.PlanAction, 0, len(p.Actions))
	for i := range p.Actions {
		pa, err := planActionToPB(p.Actions[i])
		if err != nil {
			return nil, err
		}
		pbActions = append(pbActions, pa)
	}
	if p.SchemaVersion < math.MinInt32 || p.SchemaVersion > math.MaxInt32 {
		return nil, fmt.Errorf("aws iacserver: plan SchemaVersion %d out of int32 range", p.SchemaVersion)
	}
	return &pb.IaCPlan{
		Id:            p.ID,
		Actions:       pbActions,
		CreatedAt:     timeToPB(p.CreatedAt),
		DesiredHash:   p.DesiredHash,
		SchemaVersion: int32(p.SchemaVersion), //nolint:gosec // G115: range-checked above
		InputSnapshot: copyStringMap(p.InputSnapshot),
	}, nil
}

func planFromPB(p *pb.IaCPlan) (*interfaces.IaCPlan, error) {
	if p == nil {
		return nil, nil
	}
	actions := make([]interfaces.PlanAction, 0, len(p.GetActions()))
	for _, a := range p.GetActions() {
		pa, err := planActionFromPB(a)
		if err != nil {
			return nil, err
		}
		actions = append(actions, pa)
	}
	return &interfaces.IaCPlan{
		ID:            p.GetId(),
		Actions:       actions,
		CreatedAt:     timeFromPB(p.GetCreatedAt()),
		DesiredHash:   p.GetDesiredHash(),
		SchemaVersion: int(p.GetSchemaVersion()),
		InputSnapshot: copyStringMap(p.GetInputSnapshot()),
	}, nil
}

func destroyResultToPB(r *interfaces.DestroyResult) *pb.DestroyResult {
	if r == nil {
		return nil
	}
	errs := make([]*pb.ActionError, 0, len(r.Errors))
	for _, e := range r.Errors {
		errs = append(errs, &pb.ActionError{Resource: e.Resource, Action: e.Action, Error: e.Error})
	}
	return &pb.DestroyResult{Destroyed: append([]string(nil), r.Destroyed...), Errors: errs}
}

func bootstrapResultToPB(r *interfaces.BootstrapResult) *pb.BootstrapResult {
	if r == nil {
		return nil
	}
	return &pb.BootstrapResult{
		Bucket:   r.Bucket,
		Region:   r.Region,
		Endpoint: r.Endpoint,
		EnvVars:  copyStringMap(r.EnvVars),
	}
}

func sizingToPB(s *interfaces.ProviderSizing) (*pb.ProviderSizing, error) {
	if s == nil {
		return nil, nil
	}
	specsJSON, err := marshalJSONMap(s.Specs)
	if err != nil {
		return nil, err
	}
	return &pb.ProviderSizing{InstanceType: s.InstanceType, SpecsJson: specsJSON}, nil
}

func timeToPB(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timeFromPB(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

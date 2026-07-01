package provider

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"sync"

	"github.com/GoCodeAlone/workflow/interfaces"
)

type mockResourceDriver struct {
	resourceType string
	mu           sync.RWMutex
	resources    map[string]*interfaces.ResourceOutput
}

func newMockResourceDriver(resourceType string) *mockResourceDriver {
	return &mockResourceDriver{
		resourceType: resourceType,
		resources:    make(map[string]*interfaces.ResourceOutput),
	}
}

func (d *mockResourceDriver) ResourceType() string { return d.resourceType }

func (d *mockResourceDriver) Create(_ context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := d.outputFromSpec(spec, "running")
	d.resources[spec.Name] = cloneResourceOutput(out)
	return out, nil
}

func (d *mockResourceDriver) Read(_ context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out, ok := d.resources[ref.Name]
	if !ok {
		return nil, fmt.Errorf("mock aws: %s %q not found", d.resourceType, ref.Name)
	}
	return cloneResourceOutput(out), nil
}

func (d *mockResourceDriver) Update(_ context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.resources[ref.Name]; !ok {
		return nil, fmt.Errorf("mock aws: %s %q not found", d.resourceType, ref.Name)
	}
	updateSpec := spec
	updateSpec.Name = ref.Name
	out := d.outputFromSpec(updateSpec, "running")
	d.resources[ref.Name] = cloneResourceOutput(out)
	return out, nil
}

func (d *mockResourceDriver) Delete(_ context.Context, ref interfaces.ResourceRef) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.resources, ref.Name)
	return nil
}

func (d *mockResourceDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}

	var changes []interfaces.FieldChange
	for key, want := range desired.Config {
		if got, ok := current.Outputs[key]; !ok || !reflect.DeepEqual(got, want) {
			changes = append(changes, interfaces.FieldChange{
				Path: key,
				Old:  got,
				New:  want,
			})
		}
	}
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *mockResourceDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: out.Status == "running", Message: out.Status}, nil
}

func (d *mockResourceDriver) Scale(_ context.Context, ref interfaces.ResourceRef, replicas int) (*interfaces.ResourceOutput, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	out, ok := d.resources[ref.Name]
	if !ok {
		return nil, fmt.Errorf("mock aws: %s %q not found", d.resourceType, ref.Name)
	}
	next := cloneResourceOutput(out)
	next.Outputs["replicas"] = replicas
	next.Outputs["desired_count"] = replicas
	next.Outputs["running_count"] = replicas
	d.resources[ref.Name] = cloneResourceOutput(next)
	return cloneResourceOutput(next), nil
}

func (d *mockResourceDriver) SensitiveKeys() []string { return nil }

func (d *mockResourceDriver) outputFromSpec(spec interfaces.ResourceSpec, status string) *interfaces.ResourceOutput {
	outputs := maps.Clone(spec.Config)
	if outputs == nil {
		outputs = make(map[string]any)
	}
	outputs["status"] = status
	if _, ok := outputs["replicas"]; !ok {
		outputs["replicas"] = 1
	}
	outputs["desired_count"] = outputs["replicas"]
	outputs["running_count"] = outputs["replicas"]

	return &interfaces.ResourceOutput{
		Name:       spec.Name,
		Type:       d.resourceType,
		ProviderID: fmt.Sprintf("mock-aws:%s:%s", d.resourceType, spec.Name),
		Outputs:    outputs,
		Status:     status,
	}
}

func cloneResourceOutput(out *interfaces.ResourceOutput) *interfaces.ResourceOutput {
	if out == nil {
		return nil
	}
	clone := *out
	clone.Outputs = maps.Clone(out.Outputs)
	clone.Sensitive = maps.Clone(out.Sensitive)
	return &clone
}

var _ interfaces.ResourceDriver = (*mockResourceDriver)(nil)

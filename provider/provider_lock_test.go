package provider

import (
	"context"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow/interfaces"
)

type blockingReadDriver struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingReadDriver() *blockingReadDriver {
	return &blockingReadDriver{entered: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingReadDriver) Create(context.Context, interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	return nil, nil
}
func (d *blockingReadDriver) Read(_ context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	close(d.entered)
	<-d.release
	return &interfaces.ResourceOutput{Name: ref.Name, Type: ref.Type, ProviderID: ref.ProviderID, Status: "active"}, nil
}
func (d *blockingReadDriver) Update(context.Context, interfaces.ResourceRef, interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	return nil, nil
}
func (d *blockingReadDriver) Delete(context.Context, interfaces.ResourceRef) error { return nil }
func (d *blockingReadDriver) Diff(context.Context, interfaces.ResourceSpec, *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	return &interfaces.DiffResult{}, nil
}
func (d *blockingReadDriver) HealthCheck(context.Context, interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	return &interfaces.HealthResult{Healthy: true}, nil
}
func (d *blockingReadDriver) Scale(context.Context, interfaces.ResourceRef, int) (*interfaces.ResourceOutput, error) {
	return nil, nil
}
func (d *blockingReadDriver) SensitiveKeys() []string { return nil }

func TestAWSProviderStatusDoesNotHoldProviderLockDuringDriverRead(t *testing.T) {
	driver := newBlockingReadDriver()
	p := &AWSProvider{
		initialized: true,
		driverMap: map[string]interfaces.ResourceDriver{
			"infra.test": driver,
		},
	}

	statusDone := make(chan error, 1)
	go func() {
		_, err := p.Status(context.Background(), []interfaces.ResourceRef{{
			Name: "resource-1", Type: "infra.test", ProviderID: "provider-1",
		}})
		statusDone <- err
	}()

	select {
	case <-driver.entered:
	case <-time.After(time.Second):
		close(driver.release)
		t.Fatal("Status did not reach driver Read")
	}

	writerDone := make(chan struct{})
	go func() {
		p.mu.Lock()
		p.mu.Unlock()
		close(writerDone)
	}()

	select {
	case <-writerDone:
	case <-time.After(100 * time.Millisecond):
		close(driver.release)
		t.Fatal("provider writer blocked behind Status driver Read")
	}

	close(driver.release)
	if err := <-statusDone; err != nil {
		t.Fatalf("Status: %v", err)
	}
}

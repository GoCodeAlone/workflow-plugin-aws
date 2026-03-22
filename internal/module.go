package internal

import (
	"context"

	"github.com/GoCodeAlone/workflow-plugin-aws/provider"
	"github.com/GoCodeAlone/workflow/interfaces"
)

// iacProviderModule wraps the AWS IaCProvider as a plugin ModuleInstance.
type iacProviderModule struct {
	name     string
	config   map[string]any
	provider interfaces.IaCProvider
}

func newIaCProviderModule(name string, config map[string]any) *iacProviderModule {
	return &iacProviderModule{
		name:   name,
		config: config,
	}
}

func (m *iacProviderModule) Init() error {
	m.provider = provider.NewAWSProvider()
	return m.provider.Initialize(context.Background(), m.config)
}

func (m *iacProviderModule) Start(_ context.Context) error { return nil }
func (m *iacProviderModule) Stop(_ context.Context) error  { return nil }

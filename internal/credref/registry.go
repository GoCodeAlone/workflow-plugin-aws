// Package credref implements the process-local credentials_ref: registry
// for the aws plugin. An aws.credentials Provider's CreateModule registers
// the parsed CredInput under the module name; sibling modules (storage.s3,
// step.s3_upload, etc.) look the entry up via Resolve when their config
// carries a credentials_ref: instead of an inline credentials: block.
//
// credentials_ref names MUST be unique within a config — a duplicate
// Register returns an error rather than silently clobbering, so two
// aws.credentials modules with the same name (or one shadowing another)
// fail loudly at factory-construction time.
//
// The registry is intentionally process-global. The Reset() helper exists
// for tests only — every test that calls Register MUST also call
// `t.Cleanup(credref.Reset)` so test isolation is preserved.
package credref

import (
	"fmt"
	"sync"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
)

var (
	mu       sync.RWMutex
	registry = map[string]awscreds.CredInput{}
)

// Register stores c under name. Returns an error if name was already
// registered — credentials_ref: names must be unique within a config.
func Register(name string, c awscreds.CredInput) error {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[name]; exists {
		return fmt.Errorf("credref: name %q already registered (credentials_ref names must be unique within a config)", name)
	}
	registry[name] = c
	return nil
}

// Resolve returns the CredInput registered under name and whether it was
// present. Callers MUST check the bool before using the returned value.
func Resolve(name string) (awscreds.CredInput, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// Reset clears the registry. Test-only — production code never calls this.
// Tests that call Register MUST `t.Cleanup(credref.Reset)` to avoid
// polluting other tests in the same package.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]awscreds.CredInput{}
}

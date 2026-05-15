package credref

import (
	"sync"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-aws/internal/awscreds"
)

func TestRegister_FirstCallSucceeds(t *testing.T) {
	t.Cleanup(Reset)
	if err := Register("primary", awscreds.CredInput{Region: "us-east-1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRegister_DuplicateNameErrors(t *testing.T) {
	t.Cleanup(Reset)
	if err := Register("dup", awscreds.CredInput{Region: "us-east-1"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := Register("dup", awscreds.CredInput{Region: "us-west-2"}); err == nil {
		t.Fatal("expected error on duplicate Register; got nil")
	}
}

func TestResolve_RoundTrip(t *testing.T) {
	t.Cleanup(Reset)
	want := awscreds.CredInput{
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Source:    "static",
	}
	if err := Register("rt", want); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := Resolve("rt")
	if !ok {
		t.Fatal("Resolve(rt): not found")
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestResolve_MissingReturnsZeroAndFalse(t *testing.T) {
	t.Cleanup(Reset)
	got, ok := Resolve("nope")
	if ok {
		t.Fatal("Resolve(nope): expected ok=false")
	}
	if got != (awscreds.CredInput{}) {
		t.Errorf("missing entry should be zero-value, got %+v", got)
	}
}

func TestReset_ClearsRegistry(t *testing.T) {
	if err := Register("ephemeral", awscreds.CredInput{Region: "us-east-1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	Reset()
	if _, ok := Resolve("ephemeral"); ok {
		t.Error("Reset did not clear the entry")
	}
}

func TestConcurrentRegisterResolve_RaceClean(t *testing.T) {
	t.Cleanup(Reset)
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = Register(fmtName(i), awscreds.CredInput{Region: fmtName(i)})
		}()
		go func() {
			defer wg.Done()
			_, _ = Resolve(fmtName(i))
		}()
	}
	wg.Wait()
}

func fmtName(i int) string {
	const hex = "0123456789abcdef"
	return "k-" + string([]byte{hex[(i>>4)&0xf], hex[i&0xf]})
}

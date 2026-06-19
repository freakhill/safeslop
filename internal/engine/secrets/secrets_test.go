package secrets

import (
	"context"
	"testing"
)

func TestResolveEnvRef(t *testing.T) {
	t.Setenv("SAFESLOP_TEST_SECRET", "s3cr3t")
	v, err := Resolve(context.Background(), "env:SAFESLOP_TEST_SECRET")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v != "s3cr3t" {
		t.Fatalf("value = %q, want s3cr3t", v)
	}
}

func TestResolveEnvRefUnset(t *testing.T) {
	if _, err := Resolve(context.Background(), "env:SAFESLOP_DEFINITELY_UNSET_VAR"); err == nil {
		t.Fatal("expected an error for an unset env var")
	}
}

func TestResolveUnsupportedRef(t *testing.T) {
	if _, err := Resolve(context.Background(), "plaintext"); err == nil {
		t.Fatal("expected an error for an unsupported ref scheme")
	}
}

func TestResolveMapEnv(t *testing.T) {
	t.Setenv("SAFESLOP_A", "aa")
	t.Setenv("SAFESLOP_B", "bb")
	got, err := ResolveMap(context.Background(), map[string]string{
		"A": "env:SAFESLOP_A",
		"B": "env:SAFESLOP_B",
	})
	if err != nil {
		t.Fatalf("ResolveMap: %v", err)
	}
	if got["A"] != "aa" || got["B"] != "bb" {
		t.Fatalf("got %v", got)
	}
}

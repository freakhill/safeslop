package vm

import "testing"

func TestImageIsPinned(t *testing.T) {
	if !imageIsPinned() {
		t.Fatalf("sourceImage must be digest-pinned, got %q", sourceImage)
	}
}

func TestSessionNamePerProfile(t *testing.T) {
	if sessionName("review") != "slop-vm-review" {
		t.Fatalf("got %q", sessionName("review"))
	}
}

func TestAvailableFalseWithoutTart(t *testing.T) {
	t.Setenv("PATH", "")
	if Available() {
		t.Fatal("Available must be false when tart is not on PATH")
	}
}

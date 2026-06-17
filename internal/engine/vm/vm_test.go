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

func TestBytesTrim(t *testing.T) {
	for in, want := range map[string]string{"10.0.0.9\n": "10.0.0.9", "1.2.3.4\r\n": "1.2.3.4", "5.6.7.8": "5.6.7.8"} {
		if got := string(bytesTrim([]byte(in))); got != want {
			t.Fatalf("bytesTrim(%q)=%q want %q", in, got, want)
		}
	}
}

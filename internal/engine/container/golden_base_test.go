package container

import (
	"strings"
	"testing"
)

func TestGoldenBaseInstallsFishWithoutPythonDependencyClosure(t *testing.T) {
	b, err := readAsset("Dockerfile.agent")
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	// Debian bookworm's `fish` package depends on python3. The IW2 golden base must keep
	// fish available while keeping python3 out of the base image, so the Dockerfile must
	// NOT apt-install fish through its full dependency closure. Instead it downloads the
	// snapshot-verified fish .debs and extracts their payload after installing the runtime
	// libraries fish actually links against.
	if strings.Contains(df, "\n        fish \\\n") || strings.Contains(df, "\n        python3") {
		t.Fatalf("golden base must not apt-install fish/python3; fish is extracted from snapshot .debs to avoid Debian fish -> python3 dependency")
	}
	for _, want := range []string{"apt-get download fish fish-common", "dpkg-deb -x", "fish --version", "fish -lc"} {
		if !strings.Contains(df, want) {
			t.Fatalf("Dockerfile.agent missing fish-without-python guard %q", want)
		}
	}
}

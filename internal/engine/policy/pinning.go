package policy

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PinningFinding identifies an unpinned "latest" dependency reference in files where safeslop
// requires exact pins. It ports the legacy slop-pinning gate into Go so make check enforces it
// after the fish stack is deleted (specs/0047 P4.1).
type PinningFinding struct {
	Path    string
	Line    int
	Pattern string
	Text    string
}

var latestPinPatterns = []string{`:latest"`, `@latest"`, `==latest`}

// PinningFindings scans one file's bytes for the legacy latest-pattern gate: Docker/image tags,
// OCI refs, and Python equality pins that are still set to latest.
func PinningFindings(data []byte, path string) []PinningFinding {
	var out []PinningFinding
	s := bufio.NewScanner(strings.NewReader(string(data)))
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := s.Text()
		for _, pat := range latestPinPatterns {
			if strings.Contains(line, pat) {
				out = append(out, PinningFinding{Path: path, Line: lineNo, Pattern: pat, Text: strings.TrimSpace(line)})
			}
		}
	}
	return out
}

// CheckNoLatestPins walks root and scans every *.cue plus the Go-embedded agent-tools build config
// files. Markdown/spec history is intentionally out of scope; this is a runtime/build pin gate.
func CheckNoLatestPins(root string) ([]PinningFinding, error) {
	var findings []PinningFinding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".worktrees", "node_modules", ".safeslop":
				return filepath.SkipDir
			}
			return nil
		}
		if !pinningGateFile(name) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		findings = append(findings, PinningFindings(b, rel)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Path < findings[j].Path
	})
	return findings, nil
}

func pinningGateFile(name string) bool {
	if strings.HasSuffix(name, ".cue") {
		return true
	}
	switch name {
	case "agent-tools.env", "Dockerfile.agent", "Dockerfile.agent.tools", "Dockerfile.tailored", "Dockerfile":
		return true
	default:
		return false
	}
}

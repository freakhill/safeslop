package creds

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
)

var hostExecResolver = hostexec.Default

func hostCommand(ctx context.Context, argv []string, purpose string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("host helper command is empty")
	}
	return hostExecResolver().CommandContext(ctx, hostexec.CredentialSpec(argv[0], purpose), argv[1:]...)
}

func helperLabel(argv []string) string {
	if len(argv) == 0 {
		return "host helper"
	}
	label := argv[0]
	if len(argv) > 1 {
		label += " " + argv[1]
	}
	return label
}

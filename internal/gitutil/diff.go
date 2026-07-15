package gitutil

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// Diff returns git diff for the workspace. Empty string means no changes or not a git repo.
func Diff(ctx context.Context, workspace string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--binary")
	cmd.Dir = workspace
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return out.String()
}

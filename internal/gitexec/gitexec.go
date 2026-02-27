package gitexec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes git commands with GIT_* variables scrubbed from the environment.
type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", cwd}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = scrubGitEnv(os.Environ())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(cmdArgs, " "), errMsg)
	}

	return stdout.Bytes(), nil
}

func (r *Runner) RunText(ctx context.Context, cwd string, args ...string) (string, error) {
	out, err := r.Run(ctx, cwd, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func scrubGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

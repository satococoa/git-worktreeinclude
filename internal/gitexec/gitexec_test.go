package gitexec

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrimTrailingEOL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "lf", in: "value\n", want: "value"},
		{name: "crlf", in: "value\r\n", want: "value"},
		{name: "multiple lines", in: "a\n\n", want: "a"},
		{name: "preserve spaces", in: " value with spaces \n", want: " value with spaces "},
		{name: "preserve tabs", in: "\tvalue\t\n", want: "\tvalue\t"},
		{name: "no eol", in: "value", want: "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimTrailingEOL(tt.in)
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestScrubGitEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/tmp/bad",
		"GIT_WORK_TREE=/tmp/tree",
		"HOME=/tmp/home",
		"NOEQUAL",
	}

	out := scrubGitEnv(in)
	for _, kv := range out {
		if strings.HasPrefix(kv, "GIT_") {
			t.Fatalf("GIT_* variable should be removed, got %q", kv)
		}
	}

	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/tmp/home") {
		t.Fatalf("non-GIT variables should be preserved: %v", out)
	}
}

func TestRunnerRunTextScrubsGitEnv(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")

	// If these are not scrubbed, git commands in a valid repo will fail.
	t.Setenv("GIT_DIR", filepath.Join(repo, "definitely-not-a-git-dir"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(repo, "definitely-not-a-work-tree"))

	r := NewRunner()
	got, err := r.RunText(context.Background(), repo, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("RunText returned error: %v", err)
	}
	if realPath(t, got) != realPath(t, repo) {
		t.Fatalf("unexpected repo root: got %q want %q", got, repo)
	}
}

func TestRunnerRunIncludesCommandAndStderr(t *testing.T) {
	nonRepo := t.TempDir()

	r := NewRunner()
	_, err := r.Run(context.Background(), nonRepo, "rev-parse", "--show-toplevel")
	if err == nil {
		t.Fatalf("expected error for non-repository")
	}

	msg := err.Error()
	if !strings.Contains(msg, "git -C") {
		t.Fatalf("error should include command context, got %q", msg)
	}
	if !strings.Contains(msg, "not a git repository") {
		t.Fatalf("error should include git stderr, got %q", msg)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(rp)
}

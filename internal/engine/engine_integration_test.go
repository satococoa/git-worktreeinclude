package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
)

type engineFixture struct {
	root string
	wt   string
}

func TestEngineApplyCopiesIgnoredFiles(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: ".worktreeinclude",
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.Copied != 2 {
		t.Fatalf("expected 2 copied files, got %+v", res.Summary)
	}

	gotEnv, err := os.ReadFile(filepath.Join(fx.wt, ".env"))
	if err != nil {
		t.Fatalf("read copied .env: %v", err)
	}
	if string(gotEnv) != "SOURCE_ENV\n" {
		t.Fatalf("unexpected .env content: %q", gotEnv)
	}

	for _, a := range res.Actions {
		if a.Path == "README.md" {
			t.Fatalf("tracked file must not be copied")
		}
	}
}

func TestEngineApplyConflictAndForce(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	writeFile(t, filepath.Join(fx.wt, ".env.local"), "TARGET_LOCAL\n")
	_, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: ".worktreeinclude",
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.Conflict {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.Conflict)
	}

	gotConflict, err := os.ReadFile(filepath.Join(fx.wt, ".env.local"))
	if err != nil {
		t.Fatalf("read conflict target .env.local: %v", err)
	}
	if string(gotConflict) != "TARGET_LOCAL\n" {
		t.Fatalf("target should remain unchanged on conflict, got %q", gotConflict)
	}

	_, code, err = e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: ".worktreeinclude",
		Force:   true,
	})
	if err != nil {
		t.Fatalf("Apply --force returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply --force exit code = %d, want %d", code, exitcode.OK)
	}

	gotForced, err := os.ReadFile(filepath.Join(fx.wt, ".env.local"))
	if err != nil {
		t.Fatalf("read forced .env.local: %v", err)
	}
	if string(gotForced) != "SOURCE_LOCAL\n" {
		t.Fatalf("target should be overwritten with --force, got %q", gotForced)
	}
}

func TestEngineApplyIncludeValidationAndNoop(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: ".missing-worktreeinclude",
	})
	if err != nil {
		t.Fatalf("Apply with missing include returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply with missing include exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.Matched != 0 || res.Summary.Copied != 0 || len(res.Actions) != 0 {
		t.Fatalf("expected missing include no-op, got %+v", res.Summary)
	}

	outside := filepath.Join(filepath.Dir(fx.root), "outside.include")
	writeFile(t, outside, ".env\n")
	_, code, err = e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: outside,
	})
	if err == nil {
		t.Fatalf("expected error for include outside repository")
	}
	if code != exitcode.Env {
		t.Fatalf("Apply include outside exit code = %d, want %d", code, exitcode.Env)
	}
	if !strings.Contains(err.Error(), "include path must be inside repository root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEngineDoctorAndHookPath(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	report, err := e.Doctor(context.Background(), fx.wt, DoctorOptions{
		From:    "auto",
		Include: ".worktreeinclude",
	})
	if err != nil {
		t.Fatalf("Doctor returned error: %v", err)
	}
	if !report.IncludeFound {
		t.Fatalf("expected include file to be found")
	}
	if report.PatternCount != 3 {
		t.Fatalf("unexpected pattern count: got %d want 3", report.PatternCount)
	}

	runGit(t, fx.root, "config", "core.hooksPath", "../shared-hooks")
	gotHook, err := e.HookPath(context.Background(), fx.root, true)
	if err != nil {
		t.Fatalf("HookPath returned error: %v", err)
	}
	wantHook := strings.TrimSpace(runGit(t, fx.root, "rev-parse", "--path-format=absolute", "--git-path", "hooks"))
	if filepath.Clean(gotHook) != filepath.Clean(wantHook) {
		t.Fatalf("unexpected hook path: got %q want %q", gotHook, wantHook)
	}
}

func TestErrorCodeFromCLIError(t *testing.T) {
	err := &CLIError{Code: exitcode.Env, Msg: "x"}
	if got := errorCode(err); got != exitcode.Env {
		t.Fatalf("errorCode(CLIError) = %d, want %d", got, exitcode.Env)
	}
	if got := errorCode(errors.New("plain")); got != exitcode.Internal {
		t.Fatalf("errorCode(plain) = %d, want %d", got, exitcode.Internal)
	}
}

func setupEngineFixture(t *testing.T) engineFixture {
	t.Helper()

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "branch", "-M", "main")

	writeFile(t, filepath.Join(repo, "README.md"), "tracked\n")
	writeFile(t, filepath.Join(repo, ".gitignore"), ".env\n.env.local\n")
	writeFile(t, filepath.Join(repo, ".worktreeinclude"), ".env\n.env.local\nREADME.md\n")
	runGit(t, repo, "add", "README.md", ".gitignore", ".worktreeinclude")
	runGit(t, repo, "commit", "-q", "-m", "init")

	writeFile(t, filepath.Join(repo, ".env"), "SOURCE_ENV\n")
	writeFile(t, filepath.Join(repo, ".env.local"), "SOURCE_LOCAL\n")

	wt := filepath.Join(base, "wt")
	runGit(t, repo, "worktree", "add", "-q", wt, "-b", "feature")

	return engineFixture{root: repo, wt: wt}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

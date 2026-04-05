package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
)

type engineFixture struct {
	root string
	wt   string
}

const testIncludeFile = ".test.worktreeinclude"

func TestEngineApplyCopiesIgnoredFiles(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
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
		Include: testIncludeFile,
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
		Include: testIncludeFile,
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

func TestEngineApplySkipsSameContent(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	writeFile(t, filepath.Join(fx.wt, ".env"), "SOURCE_ENV\n")

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.SkippedSame != 1 {
		t.Fatalf("expected one skipped-same file, got %+v", res.Summary)
	}
	if action := findAction(t, res.Actions, ".env"); action.Status != "same" || action.Op != "skip" {
		t.Fatalf("expected .env to be skipped as same, got %+v", action)
	}
	if res.Summary.Copied != 1 {
		t.Fatalf("expected only remaining missing file to be copied, got %+v", res.Summary)
	}
}

func TestEngineApplySkipsSourceSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior and permissions vary on Windows")
	}

	fx := setupEngineFixture(t)
	e := NewEngine()

	if err := os.Remove(filepath.Join(fx.root, ".env")); err != nil {
		t.Fatalf("remove source .env: %v", err)
	}
	if err := os.Symlink(filepath.Join(fx.root, ".env.local"), filepath.Join(fx.root, ".env")); err != nil {
		t.Fatalf("create source symlink: %v", err)
	}

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.SkippedMissingSrc != 1 {
		t.Fatalf("expected symlink source to be skipped, got %+v", res.Summary)
	}
	if action := findAction(t, res.Actions, ".env"); action.Status != "symlink" || action.Op != "skip" {
		t.Fatalf("expected .env symlink to be skipped, got %+v", action)
	}
	if _, err := os.Lstat(filepath.Join(fx.wt, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink source should not be copied into target, stat err=%v", err)
	}
}

func TestEngineApplyTreatsTargetDirectoryAsConflict(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	targetPath := filepath.Join(fx.wt, ".env")
	if err := os.Mkdir(targetPath, 0o755); err != nil {
		t.Fatalf("mkdir target path: %v", err)
	}

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.Conflict {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.Conflict)
	}
	if res.Summary.Conflicts != 1 {
		t.Fatalf("expected one conflict for target directory, got %+v", res.Summary)
	}
	if action := findAction(t, res.Actions, ".env"); action.Status != "diff" || action.Op != "conflict" {
		t.Fatalf("expected directory target to register conflict, got %+v", action)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat target directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target directory should remain intact")
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
	if !strings.Contains(err.Error(), "include path must be inside source repository root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEngineApplyUsesSourceIncludeWhenTargetIncludeMissing(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	if err := os.Remove(filepath.Join(fx.wt, testIncludeFile)); err != nil {
		t.Fatalf("remove target include: %v", err)
	}

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
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
}

func TestEngineApplyNoopWhenSourceIncludeMissingEvenIfTargetHasInclude(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	if err := os.Remove(filepath.Join(fx.root, testIncludeFile)); err != nil {
		t.Fatalf("remove source include: %v", err)
	}
	writeFile(t, filepath.Join(fx.wt, testIncludeFile), ".env\n")

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.Matched != 0 || res.Summary.Copied != 0 || len(res.Actions) != 0 {
		t.Fatalf("expected source-missing include no-op, got %+v", res.Summary)
	}
	if res.IncludeFound {
		t.Fatalf("expected include to be missing")
	}
	if res.IncludeMissingHint != IncludeMissingHintSourceMissingTargetExists {
		t.Fatalf("unexpected include hint: %q", res.IncludeMissingHint)
	}
}

func TestEngineApplyReadsIncludeFileIgnoredByGlobalExcludes(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	globalIgnore := filepath.Join(t.TempDir(), "global_ignore")
	writeFile(t, globalIgnore, ".global.worktreeinclude\n")
	runGit(t, fx.root, "config", "core.excludesFile", globalIgnore)

	writeFile(t, filepath.Join(fx.root, ".global.worktreeinclude"), ".env\n")

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: ".global.worktreeinclude",
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.Summary.Copied == 0 {
		t.Fatalf("expected ignored include file to be read, got %+v", res.Summary)
	}
}

func TestEngineApplyHintsWhenTargetIncludeIsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior and permissions vary on Windows")
	}

	fx := setupEngineFixture(t)
	e := NewEngine()

	if err := os.Remove(filepath.Join(fx.root, testIncludeFile)); err != nil {
		t.Fatalf("remove source include: %v", err)
	}
	if err := os.Remove(filepath.Join(fx.wt, testIncludeFile)); err != nil {
		t.Fatalf("remove target include: %v", err)
	}

	brokenTarget := filepath.Join(filepath.Dir(fx.wt), "missing.include")
	if err := os.Symlink(brokenTarget, filepath.Join(fx.wt, testIncludeFile)); err != nil {
		t.Fatalf("create target symlink include: %v", err)
	}

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if res.IncludeMissingHint != IncludeMissingHintSourceMissingTargetExists {
		t.Fatalf("expected target-only include hint, got %q", res.IncludeMissingHint)
	}
}

func TestEngineApplyDryRunIncludesMetadata(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply exit code = %d, want %d", code, exitcode.OK)
	}
	if !res.IncludeFound {
		t.Fatalf("expected include file to be found")
	}
	if res.PatternCount != 3 {
		t.Fatalf("unexpected pattern count: got %d want 3", res.PatternCount)
	}
}

func TestEngineApplyDryRunCopyPlanned(t *testing.T) {
	fx := setupEngineFixture(t)
	e := NewEngine()

	res, code, err := e.Apply(context.Background(), fx.wt, ApplyOptions{
		From:    "auto",
		Include: testIncludeFile,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Apply dry-run returned error: %v", err)
	}
	if code != exitcode.OK {
		t.Fatalf("Apply dry-run exit code = %d, want %d", code, exitcode.OK)
	}
	if !res.DryRun {
		t.Fatalf("expected DryRun=true in result")
	}
	if res.Summary.CopyPlanned == 0 {
		t.Fatalf("expected CopyPlanned > 0 in dry-run summary, got %+v", res.Summary)
	}
	if res.Summary.Copied != 0 {
		t.Fatalf("expected Copied=0 in dry-run summary, got %+v", res.Summary)
	}

	for _, a := range res.Actions {
		if a.Op == "copy" && a.Status != "planned" {
			t.Fatalf("expected copy actions to have status=planned in dry-run, got %+v", a)
		}
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
	writeFile(t, filepath.Join(repo, testIncludeFile), ".env\n.env.local\nREADME.md\n")
	runGit(t, repo, "add", "README.md", ".gitignore", testIncludeFile)
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

func findAction(t *testing.T, actions []Action, path string) Action {
	t.Helper()
	for _, action := range actions {
		if action.Path == path {
			return action
		}
	}
	t.Fatalf("action for %s not found", path)
	return Action{}
}

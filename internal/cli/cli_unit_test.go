package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"

	"github.com/satococoa/git-worktreeinclude/internal/engine"
	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
)

const testIncludeFile = ".test.worktreeinclude"

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"unknown-subcommand"})
	if code != exitcode.Conflict {
		t.Fatalf("Run returned %d, want %d", code, exitcode.Conflict)
	}
	if !strings.Contains(stderr.String(), "No help topic for 'unknown-subcommand'") {
		t.Fatalf("stderr should contain unknown topic message: %q", stderr.String())
	}
}

func TestRunRootHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run(nil)
	if code != exitcode.OK {
		t.Fatalf("Run returned %d, want %d", code, exitcode.OK)
	}
	if !strings.Contains(stdout.String(), "NAME:") {
		t.Fatalf("stdout should contain help output: %q", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty: %q", stderr.String())
	}
}

func TestRunRootVersion(t *testing.T) {
	oldVersion := Version
	Version = "test-version"
	defer func() {
		Version = oldVersion
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"--version"})
	if code != exitcode.OK {
		t.Fatalf("Run returned %d, want %d", code, exitcode.OK)
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty: %q", stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "git-worktreeinclude version test-version" {
		t.Fatalf("stdout = %q, want %q", got, "git-worktreeinclude version test-version")
	}

	stdout.Reset()
	stderr.Reset()
	app = New(&stdout, &stderr)

	code = app.Run([]string{"-v"})
	if code != exitcode.OK {
		t.Fatalf("Run returned %d for -v, want %d", code, exitcode.OK)
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr should be empty for -v: %q", stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "git-worktreeinclude version test-version" {
		t.Fatalf("stdout for -v = %q, want %q", got, "git-worktreeinclude version test-version")
	}
}

func TestRunApplyRejectsQuietVerbose(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"apply", "--quiet", "--verbose"})
	if code != exitcode.Args {
		t.Fatalf("Run returned %d, want %d", code, exitcode.Args)
	}
	if !strings.Contains(stderr.String(), "--quiet and --verbose cannot be used together") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestFormatActionLine(t *testing.T) {
	tests := []struct {
		name   string
		action engine.Action
		force  bool
		want   string
	}{
		{
			name:   "copy planned",
			action: engine.Action{Op: "copy", Path: ".env", Status: "planned"},
			want:   "COPY      .env (dry-run)",
		},
		{
			name:   "conflict default",
			action: engine.Action{Op: "conflict", Path: ".env.local", Status: "diff"},
			want:   "CONFLICT  .env.local (differs; use --force)",
		},
		{
			name:   "skip same",
			action: engine.Action{Op: "skip", Path: ".env", Status: "same"},
			want:   "SKIP      .env (same)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatActionLine(tt.action, tt.force)
			if got != tt.want {
				t.Fatalf("formatActionLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleExitErrorPrintsPlainError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	app.handleExitError(context.Background(), &ucli.Command{Name: "x"}, errors.New("plain failure"))
	if !strings.Contains(stderr.String(), "plain failure") {
		t.Fatalf("stderr should contain plain error: %q", stderr.String())
	}
}

func TestRunApplyQuietSuppressesActionLines(t *testing.T) {
	fx := setupCLIRepoFixture(t)
	restore := chdirForTest(t, fx.wt)
	defer restore()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"apply", "--from", "auto", "--include", testIncludeFile, "--quiet"})
	if code != exitcode.OK {
		t.Fatalf("Run returned %d, want %d, stderr=%q", code, exitcode.OK, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("apply --quiet should suppress human-readable output: %q", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("apply --quiet should not emit stderr on success: %q", stderr.String())
	}
}

func TestRunApplyVerboseReportsTargetOnlyIncludeHint(t *testing.T) {
	fx := setupCLIRepoFixture(t)
	restore := chdirForTest(t, fx.wt)
	defer restore()

	if err := os.Remove(filepath.Join(fx.root, testIncludeFile)); err != nil {
		t.Fatalf("remove source include: %v", err)
	}
	writeTestFile(t, filepath.Join(fx.wt, testIncludeFile), ".env\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"apply", "--from", "auto", "--include", testIncludeFile, "--verbose"})
	if code != exitcode.OK {
		t.Fatalf("Run returned %d, want %d, stderr=%q", code, exitcode.OK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "INCLUDE file:") {
		t.Fatalf("verbose apply should include include-file status: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "No matched ignored files.") {
		t.Fatalf("verbose apply should explain noop result: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Hint: include file was not found in source worktree") {
		t.Fatalf("verbose apply should include target-only include hint: %q", stdout.String())
	}
}

type cliRepoFixture struct {
	root string
	wt   string
}

func setupCLIRepoFixture(t *testing.T) cliRepoFixture {
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

	writeTestFile(t, filepath.Join(repo, "README.md"), "tracked\n")
	writeTestFile(t, filepath.Join(repo, ".gitignore"), ".env\n.env.local\n")
	writeTestFile(t, filepath.Join(repo, testIncludeFile), ".env\n.env.local\nREADME.md\n")
	runGit(t, repo, "add", "README.md", ".gitignore", testIncludeFile)
	runGit(t, repo, "commit", "-q", "-m", "init")

	writeTestFile(t, filepath.Join(repo, ".env"), "SOURCE_ENV\n")
	writeTestFile(t, filepath.Join(repo, ".env.local"), "SOURCE_LOCAL\n")

	wt := filepath.Join(base, "wt")
	runGit(t, repo, "worktree", "add", "-q", wt, "-b", "feature")

	return cliRepoFixture{root: repo, wt: wt}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd %s: %v", wd, err)
		}
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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

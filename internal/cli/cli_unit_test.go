package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"

	"github.com/satococoa/git-worktreeinclude/internal/engine"
	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
)

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"unknown-subcommand"})
	if code != 3 {
		t.Fatalf("Run returned %d, want %d", code, 3)
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

func TestRunHookPrint(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"hook", "print", "post-checkout"})
	if code != exitcode.OK {
		t.Fatalf("Run returned %d, want %d; stderr=%s", code, exitcode.OK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "git worktreeinclude apply --from auto --quiet || true") {
		t.Fatalf("unexpected snippet: %q", stdout.String())
	}
}

func TestRunHookPrintRejectsUnsupportedName(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr)

	code := app.Run([]string{"hook", "print", "pre-commit"})
	if code != exitcode.Args {
		t.Fatalf("Run returned %d, want %d", code, exitcode.Args)
	}
	if !strings.Contains(stderr.String(), "unsupported hook name: pre-commit") {
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

func TestCodedOrDefault(t *testing.T) {
	if got := codedOrDefault(&engine.CLIError{Code: exitcode.Env, Msg: "x"}, exitcode.Internal); got != exitcode.Env {
		t.Fatalf("codedOrDefault(CLIError) = %d, want %d", got, exitcode.Env)
	}
	if got := codedOrDefault(nil, exitcode.Internal); got != exitcode.Internal {
		t.Fatalf("codedOrDefault(nil) = %d, want %d", got, exitcode.Internal)
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

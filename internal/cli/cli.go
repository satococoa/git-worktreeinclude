package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/satococoa/git-worktreeinclude/internal/engine"
	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
	"github.com/satococoa/git-worktreeinclude/internal/hooks"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
	engine *engine.Engine
}

func New(stdout, stderr io.Writer) *App {
	return &App{
		stdout: stdout,
		stderr: stderr,
		engine: engine.NewEngine(),
	}
}

func (a *App) Run(args []string) int {
	if len(args) == 0 {
		return a.runApply(nil)
	}

	if strings.HasPrefix(args[0], "-") {
		return a.runApply(args)
	}

	switch args[0] {
	case "apply":
		return a.runApply(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "hook":
		return a.runHook(args[1:])
	case "help", "-h", "--help":
		a.printRootUsage()
		return exitcode.OK
	default:
		fmt.Fprintf(a.stderr, "unknown subcommand: %s\n\n", args[0])
		a.printRootUsage()
		return exitcode.Args
	}
}

func (a *App) runApply(args []string) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	from := fs.String("from", "auto", "source worktree path or 'auto'")
	include := fs.String("include", ".worktreeinclude", "path to include file")
	dryRun := fs.Bool("dry-run", false, "show planned actions without copying")
	force := fs.Bool("force", false, "overwrite differing target files")
	jsonOut := fs.Bool("json", false, "emit JSON output")
	quiet := fs.Bool("quiet", false, "suppress human-readable output")
	verbose := fs.Bool("verbose", false, "enable verbose output")
	fs.Usage = func() { a.printApplyUsage() }

	if err := fs.Parse(args); err != nil {
		return exitcode.Args
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.stderr, "apply does not accept positional arguments")
		a.printApplyUsage()
		return exitcode.Args
	}
	if *quiet && *verbose {
		fmt.Fprintln(a.stderr, "--quiet and --verbose cannot be used together")
		return exitcode.Args
	}
	if *from == "" {
		fmt.Fprintln(a.stderr, "--from must not be empty")
		return exitcode.Args
	}

	result, code, err := a.engine.Apply(context.Background(), mustGetwd(), engine.ApplyOptions{
		From:    *from,
		Include: *include,
		DryRun:  *dryRun,
		Force:   *force,
	})
	if err != nil {
		a.printCodedError(err)
		return code
	}

	if *jsonOut {
		enc := json.NewEncoder(a.stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(a.stderr, "failed to write JSON: %v\n", err)
			return exitcode.Internal
		}
		return code
	}

	if !*quiet {
		fmt.Fprintf(a.stdout, "APPLY from: %s\n", result.From)
		fmt.Fprintf(a.stdout, "APPLY to:   %s\n", result.To)
		if result.Summary.Matched == 0 {
			fmt.Fprintln(a.stdout, "No matched ignored files.")
		}
		for _, action := range result.Actions {
			fmt.Fprintln(a.stdout, formatActionLine(action, *force))
		}
		if *verbose || result.Summary.Matched > 0 {
			fmt.Fprintf(
				a.stdout,
				"SUMMARY matched=%d copied=%d skipped_same=%d skipped_missing_src=%d conflicts=%d errors=%d\n",
				result.Summary.Matched,
				result.Summary.Copied,
				result.Summary.SkippedSame,
				result.Summary.SkippedMissingSrc,
				result.Summary.Conflicts,
				result.Summary.Errors,
			)
		}
	}

	return code
}

func (a *App) runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	from := fs.String("from", "auto", "source worktree path or 'auto'")
	include := fs.String("include", ".worktreeinclude", "path to include file")
	quiet := fs.Bool("quiet", false, "suppress per-action output")
	verbose := fs.Bool("verbose", false, "enable verbose output")
	fs.Usage = func() { a.printDoctorUsage() }

	if err := fs.Parse(args); err != nil {
		return exitcode.Args
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.stderr, "doctor does not accept positional arguments")
		a.printDoctorUsage()
		return exitcode.Args
	}
	if *quiet && *verbose {
		fmt.Fprintln(a.stderr, "--quiet and --verbose cannot be used together")
		return exitcode.Args
	}
	if *from == "" {
		fmt.Fprintln(a.stderr, "--from must not be empty")
		return exitcode.Args
	}

	report, err := a.engine.Doctor(context.Background(), mustGetwd(), engine.DoctorOptions{
		From:    *from,
		Include: *include,
	})
	if err != nil {
		a.printCodedError(err)
		return codedOrDefault(err, exitcode.Internal)
	}

	fmt.Fprintf(a.stdout, "TARGET repo root: %s\n", report.TargetRoot)
	fmt.Fprintf(a.stdout, "SOURCE (--from %s): %s\n", report.FromMode, report.SourceRoot)
	if report.IncludeFound {
		fmt.Fprintf(a.stdout, "INCLUDE file: %s (patterns=%d)\n", report.IncludePath, report.PatternCount)
	} else {
		fmt.Fprintf(a.stdout, "INCLUDE file: %s (not found; no-op)\n", report.IncludePath)
	}
	fmt.Fprintf(
		a.stdout,
		"SUMMARY matched=%d copy_planned=%d conflicts=%d missing_src=%d skipped_same=%d errors=%d\n",
		report.Result.Summary.Matched,
		report.Result.Summary.Copied,
		report.Result.Summary.Conflicts,
		report.Result.Summary.SkippedMissingSrc,
		report.Result.Summary.SkippedSame,
		report.Result.Summary.Errors,
	)

	if !*quiet {
		for _, action := range report.Result.Actions {
			fmt.Fprintln(a.stdout, formatActionLine(action, false))
		}
	}
	if *verbose && report.Result.Summary.Matched == 0 {
		fmt.Fprintln(a.stdout, "No matched ignored files.")
	}

	return exitcode.OK
}

func (a *App) runHook(args []string) int {
	if len(args) == 0 {
		a.printHookUsage()
		return exitcode.Args
	}

	switch args[0] {
	case "path":
		fs := flag.NewFlagSet("hook path", flag.ContinueOnError)
		fs.SetOutput(a.stderr)
		absolute := fs.Bool("absolute", false, "print absolute hooks path")
		fs.Usage = func() {
			fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude hook path [--absolute]")
		}
		if err := fs.Parse(args[1:]); err != nil {
			return exitcode.Args
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(a.stderr, "hook path does not accept positional arguments")
			return exitcode.Args
		}

		p, err := a.engine.HookPath(context.Background(), mustGetwd(), *absolute)
		if err != nil {
			a.printCodedError(err)
			return codedOrDefault(err, exitcode.Internal)
		}
		fmt.Fprintln(a.stdout, filepath.ToSlash(p))
		return exitcode.OK

	case "print":
		if len(args) != 2 {
			fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude hook print post-checkout")
			return exitcode.Args
		}
		snippet, err := hooks.PrintSnippet(args[1])
		if err != nil {
			fmt.Fprintln(a.stderr, err.Error())
			return exitcode.Args
		}
		fmt.Fprint(a.stdout, snippet)
		return exitcode.OK

	default:
		fmt.Fprintf(a.stderr, "unknown hook subcommand: %s\n", args[0])
		a.printHookUsage()
		return exitcode.Args
	}
}

func formatActionLine(action engine.Action, force bool) string {
	switch action.Op {
	case "copy":
		if action.Status == "planned" {
			return fmt.Sprintf("COPY      %s (dry-run)", action.Path)
		}
		if action.Status == "error" {
			return fmt.Sprintf("ERROR     %s (copy failed)", action.Path)
		}
		return fmt.Sprintf("COPY      %s", action.Path)
	case "conflict":
		if force {
			return fmt.Sprintf("COPY      %s", action.Path)
		}
		return fmt.Sprintf("CONFLICT  %s (differs; use --force)", action.Path)
	case "skip":
		switch action.Status {
		case "same":
			return fmt.Sprintf("SKIP      %s (same)", action.Path)
		case "missing_src":
			return fmt.Sprintf("SKIP      %s (missing source)", action.Path)
		case "symlink":
			return fmt.Sprintf("SKIP      %s (source symlink)", action.Path)
		default:
			return fmt.Sprintf("ERROR     %s (processing failed)", action.Path)
		}
	default:
		return fmt.Sprintf("SKIP      %s", action.Path)
	}
}

func (a *App) printCodedError(err error) {
	fmt.Fprintln(a.stderr, err.Error())
}

func codedOrDefault(err error, fallback int) int {
	var coded *engine.CLIError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return fallback
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func (a *App) printRootUsage() {
	fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude [apply] [flags]")
	fmt.Fprintln(a.stderr, "       git-worktreeinclude doctor [flags]")
	fmt.Fprintln(a.stderr, "       git-worktreeinclude hook path [--absolute]")
	fmt.Fprintln(a.stderr, "       git-worktreeinclude hook print post-checkout")
}

func (a *App) printApplyUsage() {
	fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude apply [--from auto|<path>] [--include <path>] [--dry-run] [--force] [--json] [--quiet] [--verbose]")
}

func (a *App) printDoctorUsage() {
	fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude doctor [--from auto|<path>] [--include <path>] [--quiet] [--verbose]")
}

func (a *App) printHookUsage() {
	fmt.Fprintln(a.stderr, "Usage: git-worktreeinclude hook path [--absolute]")
	fmt.Fprintln(a.stderr, "       git-worktreeinclude hook print post-checkout")
}

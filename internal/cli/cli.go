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
		writef(a.stderr, "unknown subcommand: %s\n\n", args[0])
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
		writeln(a.stderr, "apply does not accept positional arguments")
		a.printApplyUsage()
		return exitcode.Args
	}
	if *quiet && *verbose {
		writeln(a.stderr, "--quiet and --verbose cannot be used together")
		return exitcode.Args
	}
	if *from == "" {
		writeln(a.stderr, "--from must not be empty")
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
			writef(a.stderr, "failed to write JSON: %v\n", err)
			return exitcode.Internal
		}
		return code
	}

	if !*quiet {
		writef(a.stdout, "APPLY from: %s\n", result.From)
		writef(a.stdout, "APPLY to:   %s\n", result.To)
		if *verbose {
			writeln(
				a.stdout,
				formatIncludeStatusLine(
					result.ResolvedIncludePath,
					result.IncludeFound,
					result.IncludeOrigin,
					result.IncludeMissingHint,
					result.TargetIncludePath,
					result.PatternCount,
				),
			)
		}
		if result.Summary.Matched == 0 {
			writeln(a.stdout, "No matched ignored files.")
			if !result.IncludeFound && result.IncludeMissingHint == "source_missing_target_exists" {
				if result.TargetIncludePath != "" {
					writef(
						a.stdout,
						"Hint: include file was not found in source worktree and was found only at target path: %s\n",
						result.TargetIncludePath,
					)
				} else {
					writeln(a.stdout, "Hint: include file was not found in source worktree and exists only in target worktree.")
				}
			}
		}
		for _, action := range result.Actions {
			writeln(a.stdout, formatActionLine(action, *force))
		}
		if *verbose || result.Summary.Matched > 0 {
			writef(
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
		writeln(a.stderr, "doctor does not accept positional arguments")
		a.printDoctorUsage()
		return exitcode.Args
	}
	if *quiet && *verbose {
		writeln(a.stderr, "--quiet and --verbose cannot be used together")
		return exitcode.Args
	}
	if *from == "" {
		writeln(a.stderr, "--from must not be empty")
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

	writef(a.stdout, "TARGET repo root: %s\n", report.TargetRoot)
	writef(a.stdout, "SOURCE (--from %s): %s\n", report.FromMode, report.SourceRoot)
	writeln(
		a.stdout,
		formatIncludeStatusLine(
			report.IncludePath,
			report.IncludeFound,
			report.IncludeOrigin,
			report.IncludeHint,
			report.TargetIncludePath,
			report.PatternCount,
		),
	)
	writef(
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
			writeln(a.stdout, formatActionLine(action, false))
		}
	}
	if *verbose && report.Result.Summary.Matched == 0 {
		writeln(a.stdout, "No matched ignored files.")
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
			writeln(a.stderr, "Usage: git-worktreeinclude hook path [--absolute]")
		}
		if err := fs.Parse(args[1:]); err != nil {
			return exitcode.Args
		}
		if fs.NArg() != 0 {
			writeln(a.stderr, "hook path does not accept positional arguments")
			return exitcode.Args
		}

		p, err := a.engine.HookPath(context.Background(), mustGetwd(), *absolute)
		if err != nil {
			a.printCodedError(err)
			return codedOrDefault(err, exitcode.Internal)
		}
		writeln(a.stdout, filepath.ToSlash(p))
		return exitcode.OK

	case "print":
		if len(args) != 2 {
			writeln(a.stderr, "Usage: git-worktreeinclude hook print post-checkout")
			return exitcode.Args
		}
		snippet, err := hooks.PrintSnippet(args[1])
		if err != nil {
			writeln(a.stderr, err.Error())
			return exitcode.Args
		}
		write(a.stdout, snippet)
		return exitcode.OK

	default:
		writef(a.stderr, "unknown hook subcommand: %s\n", args[0])
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

func formatIncludeStatusLine(path string, found bool, origin, hint, targetPath string, patternCount int) string {
	if found {
		originLabel := "source"
		if origin == "explicit" {
			originLabel = "explicit"
		}
		return fmt.Sprintf("INCLUDE file: %s (origin=%s, patterns=%d)", path, originLabel, patternCount)
	}

	if hint == "source_missing_target_exists" {
		if targetPath != "" {
			return fmt.Sprintf("INCLUDE file: %s (not found in source; found at target path %s; no-op)", path, targetPath)
		}
		return fmt.Sprintf("INCLUDE file: %s (not found in source; target has include file; no-op)", path)
	}

	return fmt.Sprintf("INCLUDE file: %s (not found in source; no-op)", path)
}

func (a *App) printCodedError(err error) {
	writeln(a.stderr, err.Error())
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
	writeln(a.stderr, "Usage: git-worktreeinclude [apply] [flags]")
	writeln(a.stderr, "       git-worktreeinclude doctor [flags]")
	writeln(a.stderr, "       git-worktreeinclude hook path [--absolute]")
	writeln(a.stderr, "       git-worktreeinclude hook print post-checkout")
}

func (a *App) printApplyUsage() {
	writeln(a.stderr, "Usage: git-worktreeinclude apply [--from auto|<path>] [--include <path>] [--dry-run] [--force] [--json] [--quiet] [--verbose]")
}

func (a *App) printDoctorUsage() {
	writeln(a.stderr, "Usage: git-worktreeinclude doctor [--from auto|<path>] [--include <path>] [--quiet] [--verbose]")
}

func (a *App) printHookUsage() {
	writeln(a.stderr, "Usage: git-worktreeinclude hook path [--absolute]")
	writeln(a.stderr, "       git-worktreeinclude hook print post-checkout")
}

func write(w io.Writer, s string) {
	_, _ = fmt.Fprint(w, s)
}

func writeln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

func writef(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

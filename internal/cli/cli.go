package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ucli "github.com/urfave/cli/v3"

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
	cmd := a.newRootCommand()
	runArgs := append([]string{cmd.Name}, args...)
	err := cmd.Run(context.Background(), runArgs)
	if err == nil {
		return exitcode.OK
	}

	var exitErr ucli.ExitCoder
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	return exitcode.Internal
}

func (a *App) newRootCommand() *ucli.Command {
	return &ucli.Command{
		Name:           "git-worktreeinclude",
		Usage:          "apply ignored files listed in .worktreeinclude between Git worktrees",
		Writer:         a.stdout,
		ErrWriter:      a.stderr,
		OnUsageError:   a.onUsageError,
		ExitErrHandler: a.handleExitError,
		Commands: []*ucli.Command{
			a.newApplyCommand(),
			a.newDoctorCommand(),
			a.newHookCommand(),
		},
	}
}

func (a *App) newApplyCommand() *ucli.Command {
	return &ucli.Command{
		Name:         "apply",
		Usage:        "copy ignored files from source worktree to current worktree",
		OnUsageError: a.onUsageError,
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "from", Value: "auto", Usage: "source worktree path or 'auto'"},
			&ucli.StringFlag{Name: "include", Value: ".worktreeinclude", Usage: "path to include file", TakesFile: true},
			&ucli.BoolFlag{Name: "dry-run", Usage: "show planned actions without copying"},
			&ucli.BoolFlag{Name: "force", Usage: "overwrite differing target files"},
			&ucli.BoolFlag{Name: "json", Usage: "emit JSON output"},
			&ucli.BoolFlag{Name: "quiet", Usage: "suppress human-readable output"},
			&ucli.BoolFlag{Name: "verbose", Usage: "enable verbose output"},
		},
		Action: a.runApply,
	}
}

func (a *App) runApply(ctx context.Context, cmd *ucli.Command) error {
	if cmd.Args().Len() != 0 {
		return a.onUsageError(ctx, cmd, errors.New("apply does not accept positional arguments"), true)
	}

	from := cmd.String("from")
	include := cmd.String("include")
	dryRun := cmd.Bool("dry-run")
	force := cmd.Bool("force")
	jsonOut := cmd.Bool("json")
	quiet := cmd.Bool("quiet")
	verbose := cmd.Bool("verbose")

	if quiet && verbose {
		return a.onUsageError(ctx, cmd, errors.New("--quiet and --verbose cannot be used together"), true)
	}
	if from == "" {
		return a.onUsageError(ctx, cmd, errors.New("--from must not be empty"), true)
	}

	wd, err := currentWorkdir()
	if err != nil {
		return ucli.Exit(err.Error(), exitcode.Env)
	}

	result, code, err := a.engine.Apply(ctx, wd, engine.ApplyOptions{
		From:    from,
		Include: include,
		DryRun:  dryRun,
		Force:   force,
	})
	if err != nil {
		return ucli.Exit(err, code)
	}

	if jsonOut {
		enc := json.NewEncoder(a.stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(result); err != nil {
			return ucli.Exit(fmt.Sprintf("failed to write JSON: %v", err), exitcode.Internal)
		}
		return exitWithCode(code)
	}

	if !quiet {
		writef(a.stdout, "APPLY from: %s\n", result.From)
		writef(a.stdout, "APPLY to:   %s\n", result.To)
		if verbose {
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
			if !result.IncludeFound && result.IncludeMissingHint == engine.IncludeMissingHintSourceMissingTargetExists {
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
			writeln(a.stdout, formatActionLine(action, force))
		}
		if verbose || result.Summary.Matched > 0 {
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

	return exitWithCode(code)
}

func (a *App) newDoctorCommand() *ucli.Command {
	return &ucli.Command{
		Name:         "doctor",
		Usage:        "print dry-run diagnostics",
		OnUsageError: a.onUsageError,
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "from", Value: "auto", Usage: "source worktree path or 'auto'"},
			&ucli.StringFlag{Name: "include", Value: ".worktreeinclude", Usage: "path to include file", TakesFile: true},
			&ucli.BoolFlag{Name: "quiet", Usage: "suppress per-action output"},
			&ucli.BoolFlag{Name: "verbose", Usage: "enable verbose output"},
		},
		Action: a.runDoctor,
	}
}

func (a *App) runDoctor(ctx context.Context, cmd *ucli.Command) error {
	if cmd.Args().Len() != 0 {
		return a.onUsageError(ctx, cmd, errors.New("doctor does not accept positional arguments"), true)
	}

	from := cmd.String("from")
	include := cmd.String("include")
	quiet := cmd.Bool("quiet")
	verbose := cmd.Bool("verbose")

	if quiet && verbose {
		return a.onUsageError(ctx, cmd, errors.New("--quiet and --verbose cannot be used together"), true)
	}
	if from == "" {
		return a.onUsageError(ctx, cmd, errors.New("--from must not be empty"), true)
	}

	wd, err := currentWorkdir()
	if err != nil {
		return ucli.Exit(err.Error(), exitcode.Env)
	}

	report, err := a.engine.Doctor(ctx, wd, engine.DoctorOptions{
		From:    from,
		Include: include,
	})
	if err != nil {
		return ucli.Exit(err, codedOrDefault(err, exitcode.Internal))
	}

	writef(a.stdout, "TARGET repo root: %s\n", report.TargetRoot)
	writef(a.stdout, "SOURCE (--from %s): %s\n", report.FromMode, report.SourceRoot)
	writeln(
		a.stdout,
		formatIncludeStatusLine(
			report.IncludePath,
			report.IncludeFound,
			report.IncludeOrigin,
			report.IncludeMissingHint,
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

	if !quiet {
		for _, action := range report.Result.Actions {
			writeln(a.stdout, formatActionLine(action, false))
		}
	}
	if verbose && report.Result.Summary.Matched == 0 {
		writeln(a.stdout, "No matched ignored files.")
	}

	return nil
}

func (a *App) newHookCommand() *ucli.Command {
	return &ucli.Command{
		Name:         "hook",
		Usage:        "hook helpers",
		OnUsageError: a.onUsageError,
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() == 0 {
				return a.onUsageError(ctx, cmd, errors.New("hook subcommand is required"), true)
			}
			name := cmd.Args().First()
			if cmd.Command(name) == nil {
				return a.onUsageError(ctx, cmd, fmt.Errorf("unknown hook subcommand: %s", name), true)
			}
			return nil
		},
		Commands: []*ucli.Command{
			{
				Name:         "path",
				Usage:        "print hooks path",
				OnUsageError: a.onUsageError,
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "absolute", Usage: "print absolute hooks path"},
				},
				Action: a.runHookPath,
			},
			{
				Name:         "print",
				Usage:        "print hook snippet",
				ArgsUsage:    "post-checkout",
				OnUsageError: a.onUsageError,
				Action:       a.runHookPrint,
			},
		},
	}
}

func (a *App) runHookPath(ctx context.Context, cmd *ucli.Command) error {
	if cmd.Args().Len() != 0 {
		return a.onUsageError(ctx, cmd, errors.New("hook path does not accept positional arguments"), true)
	}

	wd, err := currentWorkdir()
	if err != nil {
		return ucli.Exit(err.Error(), exitcode.Env)
	}

	p, err := a.engine.HookPath(ctx, wd, cmd.Bool("absolute"))
	if err != nil {
		return ucli.Exit(err, codedOrDefault(err, exitcode.Internal))
	}

	writeln(a.stdout, filepath.ToSlash(p))
	return nil
}

func (a *App) runHookPrint(ctx context.Context, cmd *ucli.Command) error {
	if cmd.Args().Len() != 1 {
		return a.onUsageError(ctx, cmd, errors.New("hook print requires exactly one argument: post-checkout"), true)
	}

	snippet, err := hooks.PrintSnippet(cmd.Args().First())
	if err != nil {
		return ucli.Exit(err.Error(), exitcode.Args)
	}

	write(a.stdout, snippet)
	return nil
}

func (a *App) handleExitError(_ context.Context, _ *ucli.Command, err error) {
	var exitErr ucli.ExitCoder
	if !errors.As(err, &exitErr) {
		if strings.TrimSpace(err.Error()) != "" {
			writeln(a.stderr, err.Error())
		}
		return
	}

	if strings.TrimSpace(exitErr.Error()) == "" {
		return
	}

	writeln(a.stderr, exitErr.Error())
}

func (a *App) onUsageError(_ context.Context, cmd *ucli.Command, err error, isSubcommand bool) error {
	writef(a.stderr, "Incorrect Usage: %s\n\n", err.Error())

	root := cmd.Root()
	origWriter := root.Writer
	root.Writer = a.stderr
	defer func() {
		root.Writer = origWriter
	}()

	if isSubcommand {
		_ = ucli.ShowSubcommandHelp(cmd)
	} else {
		_ = ucli.ShowRootCommandHelp(root)
	}

	return ucli.Exit("", exitcode.Args)
}

func exitWithCode(code int) error {
	if code == exitcode.OK {
		return nil
	}

	return ucli.Exit("", code)
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
		originLabel := engine.IncludeOriginSource
		if origin == engine.IncludeOriginExplicit {
			originLabel = engine.IncludeOriginExplicit
		}
		return fmt.Sprintf("INCLUDE file: %s (origin=%s, patterns=%d)", path, originLabel, patternCount)
	}

	if hint == engine.IncludeMissingHintSourceMissingTargetExists {
		if targetPath != "" {
			return fmt.Sprintf("INCLUDE file: %s (not found in source; found at target path %s; no-op)", path, targetPath)
		}
		return fmt.Sprintf("INCLUDE file: %s (not found in source; target has include file; no-op)", path)
	}

	return fmt.Sprintf("INCLUDE file: %s (not found in source; no-op)", path)
}

func codedOrDefault(err error, fallback int) int {
	var coded *engine.CLIError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return fallback
}

func currentWorkdir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to determine current working directory: %w", err)
	}
	return wd, nil
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

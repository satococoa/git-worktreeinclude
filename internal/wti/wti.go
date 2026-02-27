package wti

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/satococoa/git-worktreeinclude/internal/exitcode"
	"github.com/satococoa/git-worktreeinclude/internal/gitexec"
)

type Action struct {
	Op     string `json:"op"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

type Summary struct {
	Matched           int `json:"matched"`
	Copied            int `json:"copied"`
	SkippedSame       int `json:"skipped_same"`
	SkippedMissingSrc int `json:"skipped_missing_src"`
	Conflicts         int `json:"conflicts"`
	Errors            int `json:"errors"`
}

type Result struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	IncludeFile string   `json:"include_file"`
	Summary     Summary  `json:"summary"`
	Actions     []Action `json:"actions"`
}

type ApplyOptions struct {
	From    string
	Include string
	DryRun  bool
	Force   bool
}

type DoctorOptions struct {
	From    string
	Include string
}

type DoctorReport struct {
	TargetRoot   string
	SourceRoot   string
	FromMode     string
	IncludePath  string
	IncludeFound bool
	PatternCount int
	Result       Result
}

type Engine struct {
	git *gitexec.Runner
}

type CLIError struct {
	Code int
	Msg  string
	Err  error
}

func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Msg
	}
	if e.Msg == "" {
		return e.Err.Error()
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewEngine() *Engine {
	return &Engine{git: gitexec.NewRunner()}
}

type prepared struct {
	targetRoot   string
	sourceRoot   string
	fromMode     string
	includeArg   string
	includePath  string
	includeFound bool
	patternCount int
	matched      []string
}

func (e *Engine) Apply(ctx context.Context, cwd string, opts ApplyOptions) (Result, int, error) {
	prep, err := e.prepare(ctx, cwd, opts.From, opts.Include)
	if err != nil {
		return Result{}, errorCode(err), err
	}

	result, code := e.executePrepared(prep, opts.DryRun, opts.Force)
	return result, code, nil
}

func (e *Engine) executePrepared(prep prepared, dryRun, force bool) (Result, int) {
	result := Result{
		From:        prep.sourceRoot,
		To:          prep.targetRoot,
		IncludeFile: prep.includeArg,
		Summary: Summary{
			Matched: len(prep.matched),
		},
		Actions: make([]Action, 0, len(prep.matched)),
	}

	if !prep.includeFound {
		return result, exitcode.OK
	}

	executeCopies := !dryRun
	for _, rel := range prep.matched {
		srcPath, err := secureJoin(prep.sourceRoot, rel)
		if err != nil {
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "error"})
			result.Summary.Errors++
			continue
		}
		dstPath, err := secureJoin(prep.targetRoot, rel)
		if err != nil {
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "error"})
			result.Summary.Errors++
			continue
		}

		srcInfo, err := os.Lstat(srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "missing_src"})
				result.Summary.SkippedMissingSrc++
				continue
			}
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "error"})
			result.Summary.Errors++
			continue
		}

		if srcInfo.Mode()&os.ModeSymlink != 0 {
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "symlink"})
			result.Summary.SkippedMissingSrc++
			continue
		}
		if !srcInfo.Mode().IsRegular() {
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "missing_src"})
			result.Summary.SkippedMissingSrc++
			continue
		}

		dstInfo, err := os.Lstat(dstPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "error"})
			result.Summary.Errors++
			continue
		}

		if errors.Is(err, os.ErrNotExist) {
			status := "planned"
			if executeCopies {
				if err := copyFileAtomic(prep.targetRoot, srcPath, dstPath, srcInfo.Mode().Perm()); err != nil {
					result.Actions = append(result.Actions, Action{Op: "copy", Path: rel, Status: "error"})
					result.Summary.Errors++
					continue
				}
				status = "done"
			}
			result.Actions = append(result.Actions, Action{Op: "copy", Path: rel, Status: status})
			result.Summary.Copied++
			continue
		}

		if dstInfo.IsDir() {
			result.Actions = append(result.Actions, Action{Op: "conflict", Path: rel, Status: "diff"})
			result.Summary.Conflicts++
			continue
		}

		if dstInfo.Mode()&os.ModeSymlink == 0 {
			same, err := filesSame(srcPath, dstPath)
			if err != nil {
				result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "error"})
				result.Summary.Errors++
				continue
			}
			if same {
				result.Actions = append(result.Actions, Action{Op: "skip", Path: rel, Status: "same"})
				result.Summary.SkippedSame++
				continue
			}
		}

		if !force {
			result.Actions = append(result.Actions, Action{Op: "conflict", Path: rel, Status: "diff"})
			result.Summary.Conflicts++
			continue
		}

		status := "planned"
		if executeCopies {
			if err := copyFileAtomic(prep.targetRoot, srcPath, dstPath, srcInfo.Mode().Perm()); err != nil {
				result.Actions = append(result.Actions, Action{Op: "copy", Path: rel, Status: "error"})
				result.Summary.Errors++
				continue
			}
			status = "done"
		}
		result.Actions = append(result.Actions, Action{Op: "copy", Path: rel, Status: status})
		result.Summary.Copied++
	}

	if result.Summary.Errors > 0 {
		return result, exitcode.Internal
	}
	if result.Summary.Conflicts > 0 && !force {
		return result, exitcode.Conflict
	}
	return result, exitcode.OK
}

func (e *Engine) Doctor(ctx context.Context, cwd string, opts DoctorOptions) (DoctorReport, error) {
	prep, err := e.prepare(ctx, cwd, opts.From, opts.Include)
	if err != nil {
		return DoctorReport{}, err
	}

	res, _ := e.executePrepared(prep, true, false)

	return DoctorReport{
		TargetRoot:   prep.targetRoot,
		SourceRoot:   prep.sourceRoot,
		FromMode:     prep.fromMode,
		IncludePath:  prep.includePath,
		IncludeFound: prep.includeFound,
		PatternCount: prep.patternCount,
		Result:       res,
	}, nil
}

func (e *Engine) HookPath(ctx context.Context, cwd string, absolute bool) (string, error) {
	targetRoot, err := e.repoRoot(ctx, cwd)
	if err != nil {
		return "", err
	}
	hookPath, err := e.git.RunText(ctx, targetRoot, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", &CLIError{Code: exitcode.Env, Msg: "failed to resolve hooks path", Err: err}
	}
	if absolute && !filepath.IsAbs(hookPath) {
		hookPath = filepath.Clean(filepath.Join(targetRoot, hookPath))
	}
	return hookPath, nil
}

func (e *Engine) prepare(ctx context.Context, cwd, fromOpt, includeOpt string) (prepared, error) {
	targetRoot, err := e.repoRoot(ctx, cwd)
	if err != nil {
		return prepared{}, err
	}

	includeArg := includeOpt
	if includeArg == "" {
		includeArg = ".worktreeinclude"
	}

	includePath := includeArg
	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(targetRoot, includePath)
	}
	includePath = filepath.Clean(includePath)

	fromMode := fromOpt
	if fromMode == "" {
		fromMode = "auto"
	}

	sourceRoot, err := e.resolveSourceRoot(ctx, targetRoot, cwd, fromMode)
	if err != nil {
		return prepared{}, err
	}

	if err := e.assertSameRepository(ctx, targetRoot, sourceRoot); err != nil {
		return prepared{}, err
	}

	prep := prepared{
		targetRoot:  targetRoot,
		sourceRoot:  sourceRoot,
		fromMode:    fromMode,
		includeArg:  includeArg,
		includePath: includePath,
	}

	info, err := os.Stat(includePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prep, nil
		}
		return prepared{}, &CLIError{Code: exitcode.Env, Msg: "failed to read include file", Err: err}
	}
	if info.IsDir() {
		return prepared{}, &CLIError{Code: exitcode.Env, Msg: "include path is a directory", Err: nil}
	}

	patternCount, err := countPatterns(includePath)
	if err != nil {
		return prepared{}, &CLIError{Code: exitcode.Env, Msg: "failed to parse include file", Err: err}
	}
	prep.patternCount = patternCount
	prep.includeFound = true

	ignored, err := e.listIgnored(ctx, sourceRoot, "", true)
	if err != nil {
		return prepared{}, err
	}
	included, err := e.listIgnored(ctx, sourceRoot, includePath, false)
	if err != nil {
		return prepared{}, err
	}
	prep.matched = intersectPaths(ignored, included)
	return prep, nil
}

func (e *Engine) repoRoot(ctx context.Context, cwd string) (string, error) {
	root, err := e.git.RunText(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", &CLIError{Code: exitcode.Env, Msg: "not inside a git repository", Err: err}
	}
	return root, nil
}

func (e *Engine) resolveSourceRoot(ctx context.Context, targetRoot, cwd, from string) (string, error) {
	if from == "auto" {
		out, err := e.git.Run(ctx, targetRoot, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return "", &CLIError{Code: exitcode.Env, Msg: "failed to list worktrees", Err: err}
		}
		worktrees, err := parseWorktreePorcelainZ(out)
		if err != nil {
			return "", &CLIError{Code: exitcode.Env, Msg: "failed to parse worktree list", Err: err}
		}
		for _, wt := range worktrees {
			if wt.Bare {
				continue
			}
			if wt.Path == "" {
				continue
			}
			return filepath.Clean(wt.Path), nil
		}
		return "", &CLIError{Code: exitcode.Env, Msg: "no non-bare worktree found for --from auto", Err: nil}
	}

	sourcePath := from
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(cwd, sourcePath)
	}
	sourcePath = filepath.Clean(sourcePath)

	sourceRoot, err := e.repoRoot(ctx, sourcePath)
	if err != nil {
		return "", &CLIError{Code: exitcode.Env, Msg: "invalid --from path", Err: err}
	}
	return sourceRoot, nil
}

func (e *Engine) assertSameRepository(ctx context.Context, targetRoot, sourceRoot string) error {
	targetCommon, err := e.git.RunText(ctx, targetRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return &CLIError{Code: exitcode.Env, Msg: "failed to resolve target git common dir", Err: err}
	}
	sourceCommon, err := e.git.RunText(ctx, sourceRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return &CLIError{Code: exitcode.Env, Msg: "failed to resolve source git common dir", Err: err}
	}

	if filepath.Clean(targetCommon) != filepath.Clean(sourceCommon) {
		return &CLIError{Code: exitcode.Env, Msg: "source and target are not from the same repository", Err: nil}
	}
	return nil
}

func (e *Engine) listIgnored(ctx context.Context, repoRoot, includePath string, excludeStandard bool) ([]string, error) {
	args := []string{"ls-files", "-o", "-i", "-z"}
	if excludeStandard {
		args = append(args, "--exclude-standard")
	}
	if includePath != "" {
		args = append(args, "-X", includePath)
	}
	out, err := e.git.Run(ctx, repoRoot, args...)
	if err != nil {
		msg := "failed to list ignored files"
		if includePath != "" {
			msg = "failed to apply include patterns"
		}
		return nil, &CLIError{Code: exitcode.Env, Msg: msg, Err: err}
	}
	paths, err := parseNULPaths(out)
	if err != nil {
		return nil, &CLIError{Code: exitcode.Env, Msg: "failed to parse ignored file list", Err: err}
	}
	return paths, nil
}

func countPatterns(includePath string) (int, error) {
	f, err := os.Open(includePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	count := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		count++
	}
	if err := s.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

type worktreeEntry struct {
	Path string
	Bare bool
}

func parseWorktreePorcelainZ(out []byte) ([]worktreeEntry, error) {
	parts := bytes.Split(out, []byte{0})
	entries := make([]worktreeEntry, 0)
	current := worktreeEntry{}
	hasCurrent := false

	for _, raw := range parts {
		if len(raw) == 0 {
			if hasCurrent {
				entries = append(entries, current)
				current = worktreeEntry{}
				hasCurrent = false
			}
			continue
		}

		hasCurrent = true
		line := string(raw)
		switch {
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
		case line == "bare":
			current.Bare = true
		}
	}

	if hasCurrent {
		entries = append(entries, current)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("worktree list is empty")
	}
	return entries, nil
}

func parseNULPaths(out []byte) ([]string, error) {
	parts := bytes.Split(out, []byte{0})
	seen := make(map[string]struct{}, len(parts))
	paths := make([]string, 0, len(parts))
	for _, raw := range parts {
		if len(raw) == 0 {
			continue
		}
		norm, err := normalizeRepoPath(string(raw))
		if err != nil {
			return nil, err
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		paths = append(paths, norm)
	}
	return paths, nil
}

func normalizeRepoPath(raw string) (string, error) {
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("path contains NUL")
	}
	rel := raw
	if os.PathSeparator == '\\' {
		rel = strings.ReplaceAll(rel, "\\", "/")
	}
	rel = path.Clean(rel)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("unsafe relative path: %s", raw)
	}
	return rel, nil
}

func secureJoin(root, rel string) (string, error) {
	norm, err := normalizeRepoPath(rel)
	if err != nil {
		return "", err
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(absRoot, filepath.FromSlash(norm))
	joined, err = filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	if joined != absRoot && !strings.HasPrefix(joined, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes repository root: %s", rel)
	}
	return joined, nil
}

func intersectPaths(a, b []string) []string {
	set := make(map[string]struct{}, len(a))
	for _, p := range a {
		set[p] = struct{}{}
	}
	outSet := make(map[string]struct{})
	for _, p := range b {
		if _, ok := set[p]; ok {
			outSet[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(outSet))
	for p := range outSet {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func filesSame(srcPath, dstPath string) (bool, error) {
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return false, err
	}
	dstInfo, err := os.Stat(dstPath)
	if err != nil {
		return false, err
	}

	if !srcInfo.Mode().IsRegular() || !dstInfo.Mode().IsRegular() {
		return false, nil
	}
	if srcInfo.Size() != dstInfo.Size() {
		return false, nil
	}

	srcHash, err := hashFile(srcPath)
	if err != nil {
		return false, err
	}
	dstHash, err := hashFile(dstPath)
	if err != nil {
		return false, err
	}

	return bytes.Equal(srcHash[:], dstHash[:]), nil
}

func hashFile(filePath string) ([32]byte, error) {
	var zero [32]byte
	f, err := os.Open(filePath)
	if err != nil {
		return zero, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return zero, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func copyFileAtomic(targetRoot, srcPath, dstPath string, perm os.FileMode) error {
	parent := filepath.Dir(dstPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}

	if err := ensurePathWithinRoot(targetRoot, parent); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(parent, ".git-worktreeinclude-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	if _, err := io.Copy(tmp, src); err != nil {
		_ = src.Close()
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := src.Close(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}

	if err := os.Rename(tmpName, dstPath); err != nil {
		cleanup()
		return err
	}

	return nil
}

func ensurePathWithinRoot(root, candidate string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	candAbs, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}

	if real, err := filepath.EvalSymlinks(candAbs); err == nil {
		candAbs = real
	}
	if realRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = realRoot
	}

	if candAbs != rootAbs && !strings.HasPrefix(candAbs, rootAbs+string(os.PathSeparator)) {
		return fmt.Errorf("path escapes repository root")
	}
	return nil
}

func errorCode(err error) int {
	var coded *CLIError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return exitcode.Internal
}

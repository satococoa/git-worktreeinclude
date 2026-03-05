package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var testBinary string

func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	binDir, err := os.MkdirTemp("", "git-worktreeinclude-bin-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to create temp bin dir: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = os.RemoveAll(binDir)
	}()

	testBinary = filepath.Join(binDir, "git-worktreeinclude")
	build := exec.Command("go", "build", "-o", testBinary, "./cmd/git-worktreeinclude")
	build.Dir = repoRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to build test binary: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

type fixture struct {
	root string
	wt   string
}

type jsonResult struct {
	From        string `json:"from"`
	To          string `json:"to"`
	IncludeFile string `json:"include_file"`
	Summary     struct {
		Matched           int `json:"matched"`
		Copied            int `json:"copied"`
		SkippedSame       int `json:"skipped_same"`
		SkippedMissingSrc int `json:"skipped_missing_src"`
		Conflicts         int `json:"conflicts"`
		Errors            int `json:"errors"`
	} `json:"summary"`
	Actions []struct {
		Op     string `json:"op"`
		Path   string `json:"path"`
		Status string `json:"status"`
	} `json:"actions"`
}

func TestApplyAC1AC2AC6AC7(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--json")
	if code != 0 {
		t.Fatalf("apply --json exit code = %d, stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if realPath(t, res.From) != realPath(t, fx.root) {
		t.Fatalf("expected source %q, got %q", fx.root, res.From)
	}

	envPath := filepath.Join(fx.wt, ".env")
	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("expected copied .env: %v", err)
	}
	if string(envBytes) != "SOURCE_ENV\n" {
		t.Fatalf("unexpected .env content: %q", string(envBytes))
	}

	for _, a := range res.Actions {
		if a.Path == "README.md" {
			t.Fatalf("tracked file should not be copied")
		}
	}
	if res.Summary.Errors != 0 {
		t.Fatalf("expected no errors, got %d", res.Summary.Errors)
	}
}

func TestApplyAC3ConflictExit3(t *testing.T) {
	fx := setupFixture(t)
	writeFile(t, filepath.Join(fx.wt, ".env.local"), "TARGET_LOCAL\n")

	_, _, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto")
	if code != 3 {
		t.Fatalf("expected exit code 3, got %d", code)
	}

	got, err := os.ReadFile(filepath.Join(fx.wt, ".env.local"))
	if err != nil {
		t.Fatalf("read .env.local: %v", err)
	}
	if string(got) != "TARGET_LOCAL\n" {
		t.Fatalf("target file should remain unchanged; got %q", string(got))
	}
}

func TestApplyAC4ForceOverwrite(t *testing.T) {
	fx := setupFixture(t)
	writeFile(t, filepath.Join(fx.wt, ".env.local"), "TARGET_LOCAL\n")

	_, _, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--force")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	got, err := os.ReadFile(filepath.Join(fx.wt, ".env.local"))
	if err != nil {
		t.Fatalf("read .env.local: %v", err)
	}
	if string(got) != "SOURCE_LOCAL\n" {
		t.Fatalf("force should overwrite; got %q", string(got))
	}
}

func TestApplyAC5DryRun(t *testing.T) {
	fx := setupFixture(t)

	if err := os.Remove(filepath.Join(fx.wt, ".env")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove .env: %v", err)
	}

	_, _, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--dry-run")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	if _, err := os.Stat(filepath.Join(fx.wt, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run should not create .env")
	}
}

func TestApplyAC8MissingIncludeIsNoop(t *testing.T) {
	fx := setupFixture(t)

	stdout, _, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--include", ".missing-worktreeinclude", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Matched != 0 || res.Summary.Copied != 0 || len(res.Actions) != 0 {
		t.Fatalf("expected noop summary, got %+v", res.Summary)
	}
}

func TestApplyUsesSourceIncludeWhenTargetIncludeMissing(t *testing.T) {
	fx := setupFixture(t)
	if err := os.Remove(filepath.Join(fx.wt, ".worktreeinclude")); err != nil {
		t.Fatalf("remove target include: %v", err)
	}

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%s", code, stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Copied != 2 {
		t.Fatalf("expected source include to be used, got summary=%+v", res.Summary)
	}
}

func TestApplyNoopWhenSourceIncludeMissingEvenIfTargetHasInclude(t *testing.T) {
	fx := setupFixture(t)
	if err := os.Remove(filepath.Join(fx.root, ".worktreeinclude")); err != nil {
		t.Fatalf("remove source include: %v", err)
	}
	writeFile(t, filepath.Join(fx.wt, ".worktreeinclude"), ".env\n")

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%s", code, stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Matched != 0 || res.Summary.Copied != 0 || len(res.Actions) != 0 {
		t.Fatalf("expected source-missing include no-op, got summary=%+v", res.Summary)
	}

	humanStdout, _, humanCode := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto")
	if humanCode != 0 {
		t.Fatalf("expected human apply exit code 0, got %d", humanCode)
	}
	if !strings.Contains(humanStdout, "Hint: include file was not found in source worktree") {
		t.Fatalf("apply output missing compatibility hint: %s", humanStdout)
	}

	doctorOut, _, doctorCode := runCmd(t, fx.wt, nil, testBinary, "doctor", "--from", "auto")
	if doctorCode != 0 {
		t.Fatalf("doctor exit code = %d", doctorCode)
	}
	if !strings.Contains(doctorOut, "not found in source; found at target path") {
		t.Fatalf("doctor output missing source/target compatibility hint: %s", doctorOut)
	}
}

func TestApplyReadsIncludeFileIgnoredByGlobalExcludes(t *testing.T) {
	fx := setupFixture(t)

	globalIgnore := filepath.Join(t.TempDir(), "global_ignore")
	writeFile(t, globalIgnore, ".global.worktreeinclude\n")
	runGit(t, fx.root, "config", "core.excludesFile", globalIgnore)
	writeFile(t, filepath.Join(fx.root, ".global.worktreeinclude"), ".env\n")

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--include", ".global.worktreeinclude", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%s", code, stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Copied == 0 {
		t.Fatalf("expected ignored include file to be read, got summary=%+v", res.Summary)
	}
}

func TestApplyRejectsIncludeOutsideRepo(t *testing.T) {
	fx := setupFixture(t)

	outside := filepath.Join(filepath.Dir(fx.root), "outside.include")
	writeFile(t, outside, ".env\n")

	_, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--include", outside)
	if code != 4 {
		t.Fatalf("expected exit code 4, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "include path must be inside source repository root") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestApplyRejectsIncludeSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior and permissions vary on Windows")
	}

	fx := setupFixture(t)

	outsideDir := filepath.Join(filepath.Dir(fx.root), "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	outsideInclude := filepath.Join(outsideDir, "outside.include")
	writeFile(t, outsideInclude, ".env\n")

	linkPath := filepath.Join(fx.root, ".include-link")
	if err := os.Symlink(outsideInclude, linkPath); err != nil {
		t.Fatalf("create include symlink: %v", err)
	}

	_, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--include", ".include-link")
	if code != 4 {
		t.Fatalf("expected exit code 4, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "include path must be inside source repository root") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestApplyJSONConflictOutputContract(t *testing.T) {
	fx := setupFixture(t)
	writeFile(t, filepath.Join(fx.wt, ".env.local"), "TARGET_LOCAL\n")

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--json")
	if code != 3 {
		t.Fatalf("expected conflict exit code 3, got %d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected empty stderr for --json conflict output, got %q", stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Conflicts == 0 {
		t.Fatalf("expected conflicts > 0 in JSON summary")
	}
}

func TestApplyWithLongIncludeLine(t *testing.T) {
	fx := setupFixture(t)
	longInclude := filepath.Join(fx.root, ".long.worktreeinclude")
	longPattern := strings.Repeat("a", 70*1024)
	writeFile(t, longInclude, longPattern+"\n.env\n")

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--from", "auto", "--include", longInclude, "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0 with long include line, got %d stderr=%s", code, stderr)
	}

	res := decodeSingleJSON(t, stdout)
	if res.Summary.Copied == 0 {
		t.Fatalf("expected at least one copied file, got summary=%+v", res.Summary)
	}
}

func TestDoctorCommand(t *testing.T) {
	fx := setupFixture(t)
	stdout, _, code := runCmd(t, fx.wt, nil, testBinary, "doctor", "--from", "auto")
	if code != 0 {
		t.Fatalf("doctor exit code = %d", code)
	}
	if !strings.Contains(stdout, "TARGET repo root:") {
		t.Fatalf("doctor output missing target root: %s", stdout)
	}
	if !strings.Contains(stdout, "SUMMARY matched=") {
		t.Fatalf("doctor output missing summary: %s", stdout)
	}
	if !strings.Contains(stdout, "INCLUDE file:") {
		t.Fatalf("doctor output missing include status: %s", stdout)
	}
}

func TestHookPathAndPrint(t *testing.T) {
	fx := setupFixture(t)

	stdout, _, code := runCmd(t, fx.root, nil, testBinary, "hook", "path", "--absolute")
	if code != 0 {
		t.Fatalf("hook path exit code = %d", code)
	}
	hookPath := strings.TrimSpace(stdout)
	if !filepath.IsAbs(hookPath) {
		t.Fatalf("expected absolute hook path, got %q", hookPath)
	}

	snippet, _, code := runCmd(t, fx.root, nil, testBinary, "hook", "print", "post-checkout")
	if code != 0 {
		t.Fatalf("hook print exit code = %d", code)
	}
	if !strings.Contains(snippet, "git worktreeinclude apply --from auto --quiet || true") {
		t.Fatalf("unexpected hook snippet: %s", snippet)
	}
}

func TestHookPathAbsoluteMatchesGit(t *testing.T) {
	fx := setupFixture(t)
	runGit(t, fx.root, "config", "core.hooksPath", "../shared-hooks")

	stdout, _, code := runCmd(t, fx.root, nil, testBinary, "hook", "path", "--absolute")
	if code != 0 {
		t.Fatalf("hook path --absolute exit code = %d", code)
	}
	got := strings.TrimSpace(stdout)

	expected := strings.TrimSpace(runGit(t, fx.root, "rev-parse", "--path-format=absolute", "--git-path", "hooks"))
	if filepath.Clean(got) != filepath.Clean(expected) {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestGitExtensionInvocation(t *testing.T) {
	fx := setupFixture(t)

	binDir := filepath.Dir(testBinary)
	env := []string{fmt.Sprintf("PATH=%s%c%s", binDir, os.PathListSeparator, os.Getenv("PATH"))}
	stdout, stderr, code := runCmd(t, fx.wt, env, "git", "-C", fx.wt, "worktreeinclude", "apply", "--from", "auto", "--json")
	if code != 0 {
		t.Fatalf("git worktreeinclude apply failed: code=%d stderr=%s", code, stderr)
	}
	_ = decodeSingleJSON(t, stdout)
}

func TestRootCommandHelpAndNoImplicitApply(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary)
	if code != 0 {
		t.Fatalf("root help exit code = %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "NAME:") {
		t.Fatalf("root help output missing NAME: %s", stdout)
	}

	_, stderr, code = runCmd(t, fx.wt, nil, testBinary, "--from", "auto")
	if code != 2 {
		t.Fatalf("expected exit code 2 for root --from auto, got %d", code)
	}
	if !strings.Contains(stderr, "Incorrect Usage") {
		t.Fatalf("expected usage error for root --from auto, got: %s", stderr)
	}
}

func TestUnknownSubcommandAtRoot(t *testing.T) {
	fx := setupFixture(t)

	_, stderr, code := runCmd(t, fx.wt, nil, testBinary, "nope")
	if code != 3 {
		t.Fatalf("expected exit code 3 for unknown subcommand, got %d", code)
	}
	if !strings.Contains(stderr, "No help topic for 'nope'") {
		t.Fatalf("unexpected stderr for unknown subcommand: %s", stderr)
	}
}

func TestUsageErrorWritesHelpToStderr(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "--unknown")
	if code != 2 {
		t.Fatalf("expected exit code 2 for usage error, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "Incorrect Usage") || !strings.Contains(stderr, "NAME:") {
		t.Fatalf("stderr should contain usage header and help: %s", stderr)
	}
}

func TestHookPrintUsageErrorShowsHelpOnStderr(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "hook", "print")
	if code != 2 {
		t.Fatalf("expected exit code 2 for hook print usage error, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for hook print usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "Incorrect Usage: hook print requires exactly one argument: post-checkout") {
		t.Fatalf("stderr should contain usage error detail: %s", stderr)
	}
	if !strings.Contains(stderr, "USAGE:") {
		t.Fatalf("stderr should include help output: %s", stderr)
	}
}

func TestHookSubcommandValidationErrorsGoToStderr(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "hook")
	if code != 2 {
		t.Fatalf("expected exit code 2 for hook missing subcommand, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for hook usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "hook subcommand is required") {
		t.Fatalf("stderr should contain hook usage detail: %s", stderr)
	}
	if !strings.Contains(stderr, "USAGE:") {
		t.Fatalf("stderr should include hook help: %s", stderr)
	}

	stdout, stderr, code = runCmd(t, fx.wt, nil, testBinary, "hook", "nope")
	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown hook subcommand, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for unknown hook usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "unknown hook subcommand: nope") {
		t.Fatalf("stderr should contain unknown hook detail: %s", stderr)
	}
	if !strings.Contains(stderr, "USAGE:") {
		t.Fatalf("stderr should include hook help for unknown subcommand: %s", stderr)
	}
}

func TestApplyUsageValidationErrorsGoToStderr(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "apply", "extra")
	if code != 2 {
		t.Fatalf("expected exit code 2 for apply positional usage error, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for apply usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "apply does not accept positional arguments") {
		t.Fatalf("stderr should contain apply usage detail: %s", stderr)
	}
	if !strings.Contains(stderr, "USAGE:") {
		t.Fatalf("stderr should include apply help: %s", stderr)
	}
}

func TestDoctorUsageValidationErrorsGoToStderr(t *testing.T) {
	fx := setupFixture(t)

	stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, "doctor", "--quiet", "--verbose")
	if code != 2 {
		t.Fatalf("expected exit code 2 for doctor usage error, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout for doctor usage error, got: %q", stdout)
	}
	if !strings.Contains(stderr, "--quiet and --verbose cannot be used together") {
		t.Fatalf("stderr should contain doctor usage detail: %s", stderr)
	}
	if !strings.Contains(stderr, "USAGE:") {
		t.Fatalf("stderr should include doctor help: %s", stderr)
	}
}

func TestMergeEnvOverridesExistingKey(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/tmp/home"}
	overrides := []string{"PATH=/custom/bin:/usr/bin"}

	merged := mergeEnv(base, overrides)

	pathValue := ""
	for _, kv := range merged {
		if strings.HasPrefix(kv, "PATH=") {
			pathValue = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	if pathValue != "/custom/bin:/usr/bin" {
		t.Fatalf("expected PATH override to win, got %q", pathValue)
	}
}

func setupFixture(t *testing.T) fixture {
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

	return fixture{root: repo, wt: wt}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	stdout, stderr, code := runCmd(t, dir, nil, "git", append([]string{"-C", dir}, args...)...)
	if code != 0 {
		t.Fatalf("git %s failed: code=%d stderr=%s", strings.Join(args, " "), code, stderr)
	}
	return stdout
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runCmd(t *testing.T, dir string, env []string, name string, args ...string) (stdout string, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(os.Environ(), env)

	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return out.String(), errBuf.String(), 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.String(), errBuf.String(), exitErr.ExitCode()
	}
	t.Fatalf("failed to run command %s %v: %v", name, args, err)
	return "", "", 1
}

func mergeEnv(base []string, overrides []string) []string {
	merged := make(map[string]string, len(base)+len(overrides))
	for _, kv := range base {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		merged[parts[0]] = parts[1]
	}
	for _, kv := range overrides {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		merged[parts[0]] = parts[1]
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}

func decodeSingleJSON(t *testing.T, raw string) jsonResult {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	var res jsonResult
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, raw)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("expected single JSON object, trailing data exists: %q", raw)
	}
	return res
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(rp)
	}
	return filepath.Clean(p)
}

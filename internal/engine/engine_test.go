package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseWorktreePorcelainZ(t *testing.T) {
	data := []byte("worktree /repo/main\x00HEAD deadbeef\x00branch refs/heads/main\x00\x00worktree /repo/wt\x00HEAD cafebabe\x00branch refs/heads/feature\x00\x00")

	entries, err := parseWorktreePorcelainZ(data)
	if err != nil {
		t.Fatalf("parseWorktreePorcelainZ failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Path != "/repo/main" {
		t.Fatalf("unexpected first path: %s", entries[0].Path)
	}
	if entries[0].Bare {
		t.Fatalf("first entry should not be bare")
	}
}

func TestNormalizeRepoPath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "normal", in: ".env", want: ".env"},
		{name: "clean", in: "./foo/../bar/.env", want: "bar/.env"},
		{name: "absolute", in: "/etc/passwd", wantErr: true},
		{name: "traversal", in: "../secret", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRepoPath(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizeRepoPathBackslashBehavior(t *testing.T) {
	got, err := normalizeRepoPath(`dir\file.txt`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runtime.GOOS == "windows" {
		if got != "dir/file.txt" {
			t.Fatalf("expected slash-normalized path on Windows, got %q", got)
		}
		return
	}

	if got != `dir\file.txt` {
		t.Fatalf("expected backslash to remain a literal character on non-Windows, got %q", got)
	}
}

func TestSecureJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	if _, err := secureJoin(root, "../oops"); err == nil {
		t.Fatalf("expected traversal error")
	}
	got, err := secureJoin(root, "a/b.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Dir(got) != filepath.Join(root, "a") {
		t.Fatalf("unexpected joined path: %s", got)
	}
}

func TestFilesSame(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	c := filepath.Join(dir, "c.txt")

	if err := os.WriteFile(a, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := os.WriteFile(c, []byte("world"), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}

	same, err := filesSame(a, b)
	if err != nil {
		t.Fatalf("filesSame(a,b): %v", err)
	}
	if !same {
		t.Fatalf("expected same files")
	}

	same, err = filesSame(a, c)
	if err != nil {
		t.Fatalf("filesSame(a,c): %v", err)
	}
	if same {
		t.Fatalf("expected different files")
	}
}

func TestCountPatternsSupportsLongLine(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, ".worktreeinclude")

	longLine := strings.Repeat("a", 70*1024)
	content := "# comment\n\n" + longLine + "\n"
	if err := os.WriteFile(includePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write include file: %v", err)
	}

	count, err := countPatterns(includePath)
	if err != nil {
		t.Fatalf("countPatterns returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}

func TestEnsurePathWithinRootBoundaries(t *testing.T) {
	root := t.TempDir()

	inside := filepath.Join(root, "sub", ".worktreeinclude")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatalf("mkdir inside: %v", err)
	}
	if err := os.WriteFile(inside, []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write inside include: %v", err)
	}

	if err := ensurePathWithinRoot(root, inside); err != nil {
		t.Fatalf("inside path should be allowed, got error: %v", err)
	}

	// Guard against naive prefix checks such as /repo vs /repo2.
	outsideSibling := root + "2"
	if err := ensurePathWithinRoot(root, outsideSibling); err == nil {
		t.Fatalf("outside sibling path should be rejected")
	}
}

func TestEnsurePathWithinRootRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()

	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	escaped := filepath.Join(link, ".worktreeinclude")
	if err := os.WriteFile(filepath.Join(outside, ".worktreeinclude"), []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("write outside include: %v", err)
	}

	if err := ensurePathWithinRoot(root, escaped); err == nil {
		t.Fatalf("symlink escape path should be rejected")
	}
}

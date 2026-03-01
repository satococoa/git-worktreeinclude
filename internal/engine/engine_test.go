package engine

import (
	"os"
	"path/filepath"
	"runtime"
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

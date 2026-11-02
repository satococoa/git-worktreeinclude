package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyProtectsTrackedFilesInEitherWorktree(t *testing.T) {
	for _, tracking := range []struct {
		name           string
		source, target bool
	}{
		{"source", true, false},
		{"target", false, true},
		{"both", true, true},
	} {
		for _, deleted := range []bool{false, true} {
			for _, force := range []bool{false, true} {
				for _, dryRun := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/deleted=%t/force=%t/dry=%t", tracking.name, deleted, force, dryRun), func(t *testing.T) {
						fx := setupFixture(t)
						writeFile(t, filepath.Join(fx.root, testIncludeFile), ".env\n")
						target := filepath.Join(fx.wt, ".env")
						writeFile(t, target, "TARGET\n")
						if tracking.source {
							runGit(t, fx.root, "add", "-f", ".env")
						}
						if tracking.target {
							runGit(t, fx.wt, "add", "-f", ".env")
						}
						if deleted {
							if err := os.Remove(target); err != nil {
								t.Fatal(err)
							}
						}
						before := runGit(t, fx.wt, "status", "--porcelain=v1", "--untracked-files=all")
						args := []string{"apply", "--include", testIncludeFile, "--json"}
						if force {
							args = append(args, "--force")
						}
						if dryRun {
							args = append(args, "--dry-run")
						}
						stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, args...)
						if code != 0 || strings.TrimSpace(stderr) != "" {
							t.Fatalf("code=%d stderr=%q", code, stderr)
						}
						res := decodeSingleJSON(t, stdout)
						if res.DryRun != dryRun || res.Summary.Matched != 0 || len(res.Actions) != 0 {
							t.Fatalf("tracked files must not be planned or copied: %+v", res)
						}
						if deleted {
							if _, err := os.Lstat(target); !os.IsNotExist(err) {
								t.Fatalf("deleted file was restored: %v", err)
							}
						} else {
							data, err := os.ReadFile(target)
							if err != nil || string(data) != "TARGET\n" {
								t.Fatalf("target changed: %q, %v", data, err)
							}
						}
						if after := runGit(t, fx.wt, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
							t.Fatalf("target status changed: before=%q after=%q", before, after)
						}
					})
				}
			}
		}
	}
}

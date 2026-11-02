package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDirectoryConflictRemainsAnError(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, dryRun := range []bool{false, true} {
			for _, jsonOutput := range []bool{false, true} {
				t.Run(fmt.Sprintf("force=%t/dry=%t/json=%t", force, dryRun, jsonOutput), func(t *testing.T) {
					fx := setupFixture(t)
					writeFile(t, filepath.Join(fx.root, testIncludeFile), ".env\n.env.local\n")
					target := filepath.Join(fx.wt, ".env")
					if err := os.Mkdir(target, 0o755); err != nil {
						t.Fatal(err)
					}
					child := filepath.Join(target, "keep")
					writeFile(t, child, "KEEP\n")
					args := []string{"apply", "--include", testIncludeFile}
					if force {
						args = append(args, "--force")
					}
					if dryRun {
						args = append(args, "--dry-run")
					}
					if jsonOutput {
						args = append(args, "--json")
					}
					stdout, stderr, code := runCmd(t, fx.wt, nil, testBinary, args...)
					if code != 3 || strings.TrimSpace(stderr) != "" {
						t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
					}
					data, err := os.ReadFile(child)
					if err != nil || string(data) != "KEEP\n" {
						t.Fatalf("directory contents changed: %q, %v", data, err)
					}
					other := filepath.Join(fx.wt, ".env.local")
					if dryRun {
						if _, err := os.Lstat(other); !os.IsNotExist(err) {
							t.Fatalf("dry-run wrote other file: %v", err)
						}
					} else {
						data, err := os.ReadFile(other)
						if err != nil || string(data) != "SOURCE_LOCAL\n" {
							t.Fatalf("non-conflicting file not copied: %q, %v", data, err)
						}
					}
					if jsonOutput {
						res := decodeSingleJSON(t, stdout)
						if res.DryRun != dryRun || res.Summary.Conflicts != 1 || res.Summary.Errors != 0 || len(res.Actions) != 2 {
							t.Fatalf("incorrect conflict result: %+v", res)
						}
						if dryRun && (res.Summary.CopyPlanned != 1 || res.Summary.Copied != 0) || !dryRun && res.Summary.Copied != 1 {
							t.Fatalf("incorrect copy summary: %+v", res.Summary)
						}
						found := false
						for _, action := range res.Actions {
							if action.Path == ".env" {
								found = true
								if action.Op != "conflict" || action.Status != "diff" {
									t.Fatalf("incorrect directory action: %+v", action)
								}
							}
						}
						if !found {
							t.Fatal("directory conflict action missing")
						}
					} else {
						if !strings.Contains(stdout, "CONFLICT  .env (") || strings.Contains(stdout, "COPY      .env\n") {
							t.Fatalf("directory must be reported as conflict, not copied: %q", stdout)
						}
						if force && strings.Contains(stdout, "use --force") {
							t.Fatalf("force cannot resolve this conflict: %q", stdout)
						}
					}
				})
			}
		}
	}
}

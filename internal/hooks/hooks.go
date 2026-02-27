package hooks

import "fmt"

func PrintSnippet(name string) (string, error) {
	switch name {
	case "post-checkout":
		return `#!/bin/sh
set -eu

old="$1"
if [ "$old" = "0000000000000000000000000000000000000000" ]; then
  git worktreeinclude apply --from auto --quiet || true
fi
`, nil
	default:
		return "", fmt.Errorf("unsupported hook name: %s", name)
	}
}

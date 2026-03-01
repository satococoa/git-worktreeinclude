package hooks

import (
	"strings"
	"testing"
)

func TestPrintSnippetPostCheckout(t *testing.T) {
	got, err := PrintSnippet("post-checkout")
	if err != nil {
		t.Fatalf("PrintSnippet returned error: %v", err)
	}
	if !strings.Contains(got, "git worktreeinclude apply --from auto --quiet || true") {
		t.Fatalf("unexpected snippet: %q", got)
	}
}

func TestPrintSnippetRejectsUnsupportedHook(t *testing.T) {
	_, err := PrintSnippet("pre-commit")
	if err == nil {
		t.Fatalf("expected error for unsupported hook")
	}
}

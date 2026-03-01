package gitexec

import "testing"

func TestTrimTrailingEOL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "lf", in: "value\n", want: "value"},
		{name: "crlf", in: "value\r\n", want: "value"},
		{name: "multiple lines", in: "a\n\n", want: "a"},
		{name: "preserve spaces", in: " value with spaces \n", want: " value with spaces "},
		{name: "preserve tabs", in: "\tvalue\t\n", want: "\tvalue\t"},
		{name: "no eol", in: "value", want: "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimTrailingEOL(tt.in)
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

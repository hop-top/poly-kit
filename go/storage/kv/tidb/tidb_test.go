package tidb

import (
	"testing"
)

func TestPrefixEnd(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "normal", input: "abc", want: "abd"},
		{name: "trailing_0xff", input: "ab\xff", want: "ac"},
		{name: "multiple_trailing_0xff", input: "a\xff\xff", want: "b"},
		{name: "all_0xff_single", input: "\xff", want: ""},
		{name: "all_0xff_multi", input: "\xff\xff\xff", want: ""},
		{name: "single_byte", input: "a", want: "b"},
		{name: "null_byte", input: "\x00", want: "\x01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prefixEnd(tt.input)
			if got != tt.want {
				t.Errorf("prefixEnd(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

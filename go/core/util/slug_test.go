package util

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"foo_bar baz", "foo-bar-baz"},
		{"  leading spaces  ", "leading-spaces"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"Special!@#$chars", "special-chars"},
		{"", ""},
		{"already-slug", "already-slug"},
		{"café", "caf"},
		{"123-num", "123-num"},
		{"--leading", "leading"},
		{"trailing--", "trailing"},
		{"UPPER CASE", "upper-case"},
	}
	for _, tc := range cases {
		got := Slug(tc.in)
		if got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

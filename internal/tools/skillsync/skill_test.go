package main

import (
	"strings"
	"testing"
)

func TestSetMinVersion(t *testing.T) {
	const head = "---\nname: x\nmetadata:\n  version: \"1.0.0\" # x-release-please-version\n"
	const tail = "---\n\nbody\n"
	cases := []struct{ name, in, want string }{
		{"inserted after version", head + tail, head + "  min_version: \"0.9.0\"\n" + tail},
		{"replaced in place", head + "  min_version: \"0.1.0\"\n" + tail, head + "  min_version: \"0.9.0\"\n" + tail},
		{"replaced before version", "---\nname: x\nmetadata:\n  min_version: old\n  version: \"1.0.0\"\n" + tail,
			"---\nname: x\nmetadata:\n  min_version: \"0.9.0\"\n  version: \"1.0.0\"\n" + tail},
		{"other keys untouched", "---\nname: x\nlicense: MIT\nmetadata:\n  version: \"1.0.0\"\n  other: y\n" + tail,
			"---\nname: x\nlicense: MIT\nmetadata:\n  version: \"1.0.0\"\n  min_version: \"0.9.0\"\n  other: y\n" + tail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := setMinVersion([]byte(c.in), "0.9.0")
			if err != nil || string(got) != c.want {
				t.Fatalf("err %v\n got:\n%s\nwant:\n%s", err, got, c.want)
			}
			again, _ := setMinVersion(got, "0.9.0")
			if string(again) != string(got) {
				t.Fatalf("not idempotent:\n%s", again)
			}
		})
	}
	for name, in := range map[string]string{
		"no frontmatter":           "body\n",
		"no version":               "---\nname: x\n---\n",
		"version outside metadata": "---\nname: x\nversion: 1.0.0\n---\n",
	} {
		if _, err := setMinVersion([]byte(in), "0.9.0"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestWrapMDKeepsCodeSpansWhole(t *testing.T) {
	got := wrapMD("aaa `b b b` ccc ddd", "- ", "  ", 12)
	for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if strings.Count(l, "`")%2 != 0 {
			t.Errorf("a code span is split across lines:\n%s", got)
		}
		if len(l) > 12 && !strings.Contains(l, "`") {
			t.Errorf("line over width: %q", l)
		}
	}
	if !strings.HasPrefix(got, "- aaa") || !strings.Contains(got, "\n  ") {
		t.Errorf("prefixes wrong:\n%s", got)
	}
}

package main

import "testing"

func TestValidRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"0.5.0": true, "0.5.0-beta.16": true, "1.4.0-rc.1": true, "10.20.30": true, "0.5.0-alpha.0": true,
		"": false, "latest": false, "1": false, "1.2": false, "v1.2.3": false, "1.2.3.4": false,
		"01.2.3": false, "1.2.3-": false, "1.2.3+build": false, "1.2.3-01": false, " 1.2.3": false,
		"0.5.0a10": false, "^0.5.0": false,
	} {
		if got := validRelease(v); got != want {
			t.Errorf("validRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCompareRelease(t *testing.T) {
	// each row is strictly ordered: lower < higher
	for _, p := range [][2]string{
		{"0.5.0-alpha.9", "0.5.0-alpha.10"},
		{"0.5.0-alpha.12", "0.5.0-beta.1"},
		{"0.5.0-beta.16", "0.5.0-rc.0"},
		{"0.5.0-rc.9", "0.5.0"},
		{"0.5.0", "0.5.1"},
		{"0.5.9", "0.10.0"},
		{"0.5.0-alpha", "0.5.0-alpha.1"},
		{"0.5.0-1", "0.5.0-alpha"},
		{"0.9.9", "1.0.0-alpha.1"},
	} {
		if compareRelease(p[0], p[1]) != -1 || compareRelease(p[1], p[0]) != 1 {
			t.Errorf("%s must be older than %s", p[0], p[1])
		}
		if compareRelease(p[0], p[0]) != 0 {
			t.Errorf("%s must equal itself", p[0])
		}
	}
}

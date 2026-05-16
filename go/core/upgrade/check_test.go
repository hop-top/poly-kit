package upgrade

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.1", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", true},
		{"1.2.3", "2.0.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"1.0.0-alpha", "1.0.0", true},
		{"1.0.0", "1.0.0-alpha", false},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", true},
		{"1.0.0-alpha.2", "1.0.0-alpha.1", false},
		{"1.0.0-alpha", "1.0.0-alpha.1", true},
		{"1.0.0-alpha.1", "1.0.0-beta", true},
		{"0.0.1-alpha.1", "0.0.1", true},
		{"0.0.1", "0.0.1-alpha.1", false},
	}
	for _, c := range cases {
		got := isNewer(c.current, c.latest)
		if got != c.want {
			t.Errorf("isNewer(%q, %q) = %v; want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	v := parseSemver("1.2.3")
	if v.major != 1 || v.minor != 2 || v.patch != 3 {
		t.Errorf("unexpected: %+v", v)
	}
}

func TestSelectAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "mytool_linux_amd64.tar.gz", BrowserDownloadURL: "http://linux-amd64"},
		{Name: "mytool_darwin_arm64.tar.gz", BrowserDownloadURL: "http://darwin-arm64"},
		{Name: "mytool_darwin_amd64.tar.gz", BrowserDownloadURL: "http://darwin-amd64"},
	}
	got := selectAsset(assets)
	if got == "" {
		t.Error("selectAsset returned empty string")
	}
}

package oauth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Structural leak shapes. A cassette holds none of them, whatever the value.
var cassLeakShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	// Three base64url segments: any JWT, signed or not.
	{"jwt-shaped string", regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"jwt header", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}`)},
	{"PEM block", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)},
	{"Google access token", regexp.MustCompile(`\bya29\.[A-Za-z0-9_-]{8,}`)},
	{"Google refresh token", regexp.MustCompile(`\b1//[A-Za-z0-9_-]{8,}`)},
}

// cassLeaks returns one finding per leaked shape or runtime-generated
// sensitive value in data. Findings name the shape or value label, never the
// value.
func cassLeaks(data []byte, values map[string]string) []string {
	var out []string
	for _, sh := range cassLeakShapes {
		if sh.re.Match(data) {
			out = append(out, sh.name)
		}
	}
	for v, label := range values {
		for _, form := range []string{v, url.QueryEscape(v), base64.RawURLEncoding.EncodeToString([]byte(v))} {
			if bytes.Contains(data, []byte(form)) {
				out = append(out, "runtime value: "+label)
				break
			}
		}
	}
	return out
}

func cassSnapshot() map[string]string {
	cassMu.Lock()
	defer cassMu.Unlock()
	m := make(map[string]string, len(cassCollected))
	for k, v := range cassCollected {
		m[k] = v
	}
	return m
}

// cassAssertClean fails if any file under dir leaks.
func cassAssertClean(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	values := cassSnapshot()
	for k, v := range extra {
		values[k] = v
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, f := range cassLeaks(data, values) {
			t.Errorf("cassette %s leaks: %s", p, f)
		}
		return nil
	})
	if err != nil {
		t.Errorf("walk %s: %v", dir, err)
	}
}

// TestCassetteLeak scans every committed cassette (or, when recording, the
// fresh ones) for JWT-shaped strings, PEM blocks and every sensitive value
// generated at runtime by the scenarios that ran before it in this process.
func TestCassetteLeak(t *testing.T) {
	root := filepath.Join("testdata", "cassettes")
	ents, err := os.ReadDir(root)
	if err != nil || len(ents) == 0 {
		t.Fatalf("no cassettes under %s: %v", root, err)
	}
	cassAssertClean(t, root, nil)
}

// TestCassetteLeakDetector proves the scanner flags each shape, using values
// assembled at runtime.
func TestCassetteLeakDetector(t *testing.T) {
	seg := func() string {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	secret := "runtime-" + cassRand(t, 16)
	cases := map[string]struct {
		in   string
		want string
	}{
		"jwt":     {"body: " + "eyJ" + seg() + "." + seg() + "." + seg(), "jwt-shaped string"},
		"pem":     {"k: " + "-----BEGIN " + "PRIVATE KEY-----", "PEM block"},
		"google":  {"access_token: " + "ya29." + seg(), "Google access token"},
		"runtime": {"client_secret=" + url.QueryEscape(secret), "runtime value: probe"},
	}
	for name, c := range cases {
		got := cassLeaks([]byte(c.in), map[string]string{secret: "probe"})
		if !strings.Contains(strings.Join(got, "|"), c.want) {
			t.Errorf("%s: findings %v, want %q", name, got, c.want)
		}
	}
	clean := "url: https://login.microsoftonline.com/scrubbed-tenant-id/oauth2/v2.0/token\nbody: grant_type=client_credentials"
	if got := cassLeaks([]byte(clean), map[string]string{secret: "probe"}); len(got) != 0 {
		t.Errorf("clean text flagged: %v", got)
	}
}

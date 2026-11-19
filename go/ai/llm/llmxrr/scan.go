package llmxrr

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// TB is the part of testing.TB [AssertCassettesClean] uses.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// leakPatterns are credential shapes a committed cassette must not hold.
// A masked value ("<redacted>") matches none of them.
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`AIza[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)[?&](?:key|api_key|apikey|api-key|access_token|token)=[^<&\s"']`),
	regexp.MustCompile(`(?im)^\s*(?:authorization|proxy-authorization|x-api-key|x-goog-api-key)\s*:`),
	regexp.MustCompile(`(?i)\\?"(?:api[_-]?key|x-api-key|x-goog-api-key|authorization|access_token|secret|password)\\?"\s*:\s*\\?"[^"\\<]`),
}

// CheckNoSecrets fails when any file under dir holds a credential shape
// or one of the given exact secret values (values under 8 bytes are
// ignored). A missing dir is clean.
func CheckNoSecrets(dir string, secrets ...string) error {
	secrets = keepSecrets(secrets)
	var found []string
	err := walk(dir, func(path string, data []byte) error {
		for _, s := range secrets {
			if bytes.Contains(data, []byte(s)) {
				found = append(found, path+": holds a given secret value")
			}
		}
		for _, re := range leakPatterns {
			if re.Match(data) {
				found = append(found, fmt.Sprintf("%s: matches %s", path, re))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("llmxrr: secrets in cassettes:\n%s", strings.Join(found, "\n"))
	}
	return nil
}

// CheckNoFailures fails with [ErrStoredFailure] when an http cassette
// under dir holds a failed exchange: an error envelope or a non-2xx
// status. A missing dir is clean.
func CheckNoFailures(dir string) error {
	var found []string
	err := walk(dir, func(path string, data []byte) error {
		if !strings.HasSuffix(path, ".resp.yaml") {
			return nil
		}
		var env struct {
			Adapter string `yaml:"adapter"`
			Error   string `yaml:"error"`
			Payload struct {
				Status int `yaml:"status"`
			} `yaml:"payload"`
		}
		if err := yaml.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("llmxrr: %s: %w", path, err)
		}
		switch {
		case env.Adapter != "http":
		case env.Error != "":
			found = append(found, fmt.Sprintf("%s: recorded error: %s", path, env.Error))
		case env.Payload.Status < 200 || env.Payload.Status > 299:
			found = append(found, fmt.Sprintf("%s: status %d %s", path, env.Payload.Status, http.StatusText(env.Payload.Status)))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("%w: delete these and re-record:\n%s", ErrStoredFailure, strings.Join(found, "\n"))
	}
	return nil
}

// AssertCassettesClean fails tb when dir holds a leaked secret (see
// [CheckNoSecrets]) or a failed exchange (see [CheckNoFailures]). Call it
// from a plain test in any package that commits cassettes:
//
//	func TestCassettesClean(t *testing.T) { llmxrr.AssertCassettesClean(t, "testdata/cassettes") }
//
// secrets are exact values to look for too, such as the key used to record.
func AssertCassettesClean(tb TB, dir string, secrets ...string) {
	tb.Helper()
	if err := CheckNoSecrets(dir, secrets...); err != nil {
		tb.Fatalf("%v", err)
	}
	if err := CheckNoFailures(dir); err != nil {
		tb.Fatalf("%v", err)
	}
}

// walk calls fn with every regular file under dir.
func walk(dir string, fn func(path string, data []byte) error) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return fn(path, data)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

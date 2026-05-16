package svc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildCassette returns a valid in-memory cassette tar.gz for testing.
// Optional mutators tweak the result (corrupt manifest, bad story hash, etc.).
func buildCassette(t *testing.T, mutate func(*cassetteSpec)) []byte {
	t.Helper()
	spec := defaultSpec()
	if mutate != nil {
		mutate(&spec)
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	tw := tar.NewWriter(gw)

	writeFile := func(name string, body []byte) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}

	writeFile("manifest.yaml", []byte(spec.manifestYAML))
	writeFile("story.yaml", spec.storyBytes)
	for _, s := range spec.steps {
		writeFile(fmt.Sprintf("steps/%s/result.json", s.id),
			fmt.Appendf(nil, `{"exit_code":%d,"duration_ms":%d}`, s.exitCode, s.duration))
		writeFile(fmt.Sprintf("steps/%s/cassette/req.json", s.id), []byte(`{}`))
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return gzBuf.Bytes()
}

type stepSpec struct {
	id       string
	exitCode int
	duration int64
}

type cassetteSpec struct {
	manifestYAML string
	storyBytes   []byte
	steps        []stepSpec
}

func defaultSpec() cassetteSpec {
	story := []byte("story: hello\n")
	sum := sha256.Sum256(story)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	mf := fmt.Sprintf(`schema_version: "1"
binary: example
recorder: xrr
recorder_version: 0.1.0
recorded_at: 2026-05-11T12:00:00Z
story_ref:
  story_id: example.story
  content_hash: %q
steps:
  - id: step-1
    cassette_dir: steps/step-1/cassette
    captures: steps/step-1
`, hash)
	return cassetteSpec{
		manifestYAML: mf,
		storyBytes:   story,
		steps:        []stepSpec{{id: "step-1", exitCode: 0, duration: 42}},
	}
}

func TestCassetteReceive_HappyPath(t *testing.T) {
	body := buildCassette(t, nil)
	rc := &CassetteReceiver{}
	cas, err := rc.Receive(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	defer cas.Close()

	if cas.Manifest == nil || cas.Manifest.Binary != "example" {
		t.Fatalf("manifest unexpected: %+v", cas.Manifest)
	}
	if len(cas.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(cas.Steps))
	}
	step, ok := cas.Steps["step-1"]
	if !ok {
		t.Fatalf("missing step-1: %+v", cas.Steps)
	}
	if step.DurationMS != 42 {
		t.Fatalf("step duration: want 42, got %d", step.DurationMS)
	}
}

func TestCassetteReceive_RejectsTraversal(t *testing.T) {
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "../etc/evil", Mode: 0o644, Size: 4}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("oops"))
	_ = tw.Close()
	_ = gw.Close()

	rc := &CassetteReceiver{}
	if _, err := rc.Receive(bytes.NewReader(gzBuf.Bytes())); err == nil {
		t.Fatal("expected error on traversal entry, got nil")
	} else if !strings.Contains(err.Error(), CodeCassetteMalformed) {
		t.Fatalf("expected %s code, got: %v", CodeCassetteMalformed, err)
	}
}

func TestCassetteReceive_StoryHashMismatch(t *testing.T) {
	body := buildCassette(t, func(s *cassetteSpec) {
		// Replace storyBytes after manifest is committed; hash won't match.
		s.storyBytes = []byte("tampered\n")
	})
	rc := &CassetteReceiver{}
	if _, err := rc.Receive(bytes.NewReader(body)); err == nil {
		t.Fatal("expected story hash mismatch")
	} else if !strings.Contains(err.Error(), CodeStoryHashMismatch) {
		t.Fatalf("want %s, got: %v", CodeStoryHashMismatch, err)
	}
}

func TestCassetteReceive_ManifestInvalid(t *testing.T) {
	body := buildCassette(t, func(s *cassetteSpec) {
		s.manifestYAML = `schema_version: "2"` + "\n"
	})
	rc := &CassetteReceiver{}
	if _, err := rc.Receive(bytes.NewReader(body)); err == nil {
		t.Fatal("expected manifest invalid")
	} else if !strings.Contains(err.Error(), CodeCassetteManifestInvalid) {
		t.Fatalf("want %s, got: %v", CodeCassetteManifestInvalid, err)
	}
}

func TestCassetteReceive_HardCap(t *testing.T) {
	body := buildCassette(t, nil)
	rc := &CassetteReceiver{HardCap: 32}
	if _, err := rc.Receive(bytes.NewReader(body)); err == nil {
		t.Fatal("expected size cap to trigger")
	} else if !strings.Contains(err.Error(), CodeCassetteGzipBomb) && !strings.Contains(err.Error(), CodeCassetteMalformed) {
		t.Fatalf("want bomb or malformed code, got: %v", err)
	}
}

func TestParseScenarioRef(t *testing.T) {
	cases := []struct {
		in      string
		wantNS  string
		wantID  string
		wantVer string
		wantErr bool
	}{
		{"acme/widget", "acme", "widget", "", false},
		{"acme/widget@2026.05.07", "acme", "widget", "2026.05.07", false},
		{"acme/widget.deploy", "acme", "widget.deploy", "", false},
		{"BAD/widget", "", "", "", true},
		{"acme/", "", "", "", true},
		{"/widget", "", "", "", true},
		{"acme/widget@bad ver", "", "", "", true},
	}
	for _, tc := range cases {
		got, err := ParseScenarioRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseScenarioRef(%q): want error, got nil", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScenarioRef(%q): %v", tc.in, err)
			continue
		}
		if got.Namespace != tc.wantNS || got.ID != tc.wantID || got.Version != tc.wantVer {
			t.Errorf("ParseScenarioRef(%q): got %+v, want ns=%s id=%s ver=%s",
				tc.in, got, tc.wantNS, tc.wantID, tc.wantVer)
		}
	}
}

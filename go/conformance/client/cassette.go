package client

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest is the in-memory shape of manifest.yaml. SchemaVersion is
// emitted as a string ("1") matching design.md §2; downstream svc
// validates and rejects unsupported versions.
type Manifest struct {
	SchemaVersion    string         `yaml:"schema_version"`
	ScenarioID       string         `yaml:"scenario_id"`
	StoryPath        string         `yaml:"story_path,omitempty"`
	StoryContentHash string         `yaml:"story_content_hash,omitempty"`
	RecordedAt       time.Time      `yaml:"recorded_at,omitempty"`
	KitVersion       string         `yaml:"kit_version,omitempty"`
	XrrVersion       string         `yaml:"xrr_version,omitempty"`
	Tier             int            `yaml:"tier,omitempty"`
	Steps            []ManifestStep `yaml:"steps,omitempty"`
}

// ManifestStep is one row in manifest.yaml.steps[]. ID is the
// step-id scen's assertion.on resolves against.
type ManifestStep struct {
	ID          string          `yaml:"id"`
	CassetteDir string          `yaml:"cassette_dir,omitempty"`
	Capture     ManifestCapture `yaml:"capture,omitempty"`
}

// ManifestCapture points at the per-step capture artifacts (stdout,
// stderr, result.json) relative to the cassette dir root.
type ManifestCapture struct {
	Stdout string `yaml:"stdout,omitempty"`
	Stderr string `yaml:"stderr,omitempty"`
	Result string `yaml:"result,omitempty"`
}

// LoadManifest reads <cassetteDir>/manifest.yaml, decodes it, and
// returns the typed Manifest. Errors are wrapped as ErrManifestParse.
func LoadManifest(cassetteDir string) (*Manifest, error) {
	path := filepath.Join(cassetteDir, "manifest.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ManifestParseError("manifest.yaml not found at "+path, err.Error())
		}
		return nil, ManifestParseError("read "+path, err.Error())
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, ManifestParseError("decode "+path, err.Error())
	}
	if m.SchemaVersion == "" {
		m.SchemaVersion = CassetteSchemaVersion
	}
	return &m, nil
}

// Pack walks cassetteDir, packs it into a deterministic gzipped tar
// stream, and returns an io.ReadCloser plus the SHA-256 of the body
// bytes (used as the Idempotency-Key). The Close call is a no-op for
// the in-memory implementation; callers should still defer Close.
//
// Determinism rules (design.md §2):
//   - tar header Mode=0644, Uid=Gid=0, ModTime=epoch, no Uname/Gname
//   - gzip header writes no embedded filename / zero mtime
//   - filesystem walk emits entries in byte-lex sorted order
//   - manifest.yaml is materialized first so svc can stream-parse it
//     without buffering the whole archive
//
// maxBytes, when >0, caps the packed body. Above the cap Pack returns
// ErrCassetteTooLarge before producing any bytes.
func Pack(cassetteDir string, manifest *Manifest, maxBytes int64) (io.ReadCloser, string, error) {
	if cassetteDir == "" {
		return nil, "", CassettePackError("cassetteDir is empty", "")
	}
	info, err := os.Stat(cassetteDir)
	if err != nil {
		return nil, "", CassettePackError("stat "+cassetteDir, err.Error())
	}
	if !info.IsDir() {
		return nil, "", CassettePackError(cassetteDir+" is not a directory", "")
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, "", CassettePackError("gzip writer", err.Error())
	}
	// Zero the gzip header so identical inputs hash identically.
	gz.Name = ""
	gz.Comment = ""
	gz.ModTime = time.Time{}

	tw := tar.NewWriter(gz)

	// Materialize manifest.yaml first.
	if manifest != nil {
		manifestBytes, err := encodeManifest(manifest)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, "", CassettePackError("encode manifest", err.Error())
		}
		if err := writeTarEntry(tw, "manifest.yaml", manifestBytes); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, "", CassettePackError("write manifest.yaml", err.Error())
		}
	}

	// Walk filesystem, sorted, skipping any manifest.yaml at the root
	// (the in-memory one is authoritative) and any hidden entries.
	entries, err := sortedWalk(cassetteDir)
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, "", CassettePackError("walk "+cassetteDir, err.Error())
	}
	for _, ent := range entries {
		rel, err := filepath.Rel(cassetteDir, ent)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, "", CassettePackError("relpath "+ent, err.Error())
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.yaml" && manifest != nil {
			continue
		}
		body, err := os.ReadFile(ent)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, "", CassettePackError("read "+ent, err.Error())
		}
		if err := writeTarEntry(tw, rel, body); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, "", CassettePackError("write "+rel, err.Error())
		}
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return nil, "", CassettePackError("close tar", err.Error())
	}
	if err := gz.Close(); err != nil {
		return nil, "", CassettePackError("close gzip", err.Error())
	}

	if maxBytes > 0 && int64(buf.Len()) > maxBytes {
		return nil, "", CassetteTooLargeError(
			fmt.Sprintf("packed cassette %d bytes exceeds cap %d", buf.Len(), maxBytes))
	}

	sum := sha256.Sum256(buf.Bytes())
	key := "sha256:" + hex.EncodeToString(sum[:])

	return io.NopCloser(bytes.NewReader(buf.Bytes())), key, nil
}

// writeTarEntry writes one file entry with normalised header fields
// (mode 0644, uid/gid 0, mtime epoch, no Uname/Gname). The output is
// byte-for-byte reproducible for the same input.
func writeTarEntry(tw *tar.Writer, name string, body []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
		Uid:      0,
		Gid:      0,
		Uname:    "",
		Gname:    "",
		Format:   tar.FormatPAX,
	}
	// PAX records can encode an mtime down to nanosecond; force-zero
	// AccessTime/ChangeTime too so PAX records don't fork on platform.
	hdr.AccessTime = time.Time{}
	hdr.ChangeTime = time.Time{}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	return nil
}

// sortedWalk returns all regular file paths under root in
// byte-lex order. Skips directories themselves (we don't emit tar
// directory entries — empty dirs aren't part of the cassette
// contract) and any entry whose basename starts with ".".
func sortedWalk(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// encodeManifest serializes a Manifest to deterministic YAML. yaml.v3
// emits map keys in struct-declaration order when given a struct;
// time fields are encoded RFC3339. Empty fields with omitempty are
// elided so the byte output is reproducible across machines whose
// hostnames / locale / etc. differ.
func encodeManifest(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

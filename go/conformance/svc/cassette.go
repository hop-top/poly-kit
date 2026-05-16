package svc

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Cassette wire-format constants per design §2.
const (
	CassetteContentType = "application/vnd.kit.cassette+tar+gzip"

	// DefaultHardCap is the hard limit on uncompressed cassette size.
	// Reject anything larger.
	DefaultHardCap = int64(64 << 20)

	// DefaultSoftCap is the informational threshold; over-size cassettes
	// emit X-Kit-Cassette-Size-Warning but still grade.
	DefaultSoftCap = int64(8 << 20)
)

// Cassette is an unpacked cassette tree pinned at a temp dir on disk.
// Caller must call Close to remove the tree.
type Cassette struct {
	Root           string
	Manifest       *Manifest
	StoryBytes     []byte
	StoryHash      string // hex sha256 of StoryBytes
	UncompressedSz int64
	Steps          map[string]CassetteStep
}

// CassetteStep is a per-step pointer plus the parsed result.json.
type CassetteStep struct {
	ID          string
	CassetteDir string // absolute path under Cassette.Root
	ExitCode    int    `json:"exit_code"`
	DurationMS  int64  `json:"duration_ms"`
	Stdout      []byte
	Stderr      []byte
}

// Close removes the temp dir backing the cassette.
func (c *Cassette) Close() error {
	if c == nil || c.Root == "" {
		return nil
	}
	return os.RemoveAll(c.Root)
}

// CassetteReceiver configures how cassettes are accepted off the wire.
type CassetteReceiver struct {
	// HardCap is the maximum uncompressed bytes accepted. Zero =
	// DefaultHardCap.
	HardCap int64
	// SoftCap is the informational threshold. Zero = DefaultSoftCap.
	SoftCap int64
	// TempDir is the parent for cassette temp dirs. Empty = os.TempDir().
	TempDir string
}

// stepResultRaw is the on-disk shape for result.json.
type stepResultRaw struct {
	ExitCode   int   `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`
}

// resolved returns effective caps and temp dir.
func (rc *CassetteReceiver) resolved() (hard, soft int64, tmp string) {
	hard = rc.HardCap
	if hard <= 0 {
		hard = DefaultHardCap
	}
	soft = rc.SoftCap
	if soft <= 0 {
		soft = DefaultSoftCap
	}
	tmp = rc.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	return
}

// Receive reads a tar.gz cassette from r and unpacks into a temp dir.
// Caller must Close the returned Cassette.
//
// Enforces:
//   - Hard cap on uncompressed total (bomb defense).
//   - Strict tar entry name cleaning + traversal rejection.
//   - manifest.yaml + story.yaml required at tar root.
//   - story content hash matches manifest.story_ref.content_hash.
func (rc *CassetteReceiver) Receive(r io.Reader) (*Cassette, error) {
	hard, _, tmp := rc.resolved()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%s: gzip: %w", CodeCassetteMalformed, err)
	}
	defer gz.Close()

	root, err := os.MkdirTemp(tmp, "kit-cassette-*")
	if err != nil {
		return nil, fmt.Errorf("%s: mkdir: %w", CodeSvcInternal, err)
	}

	cas := &Cassette{
		Root:  root,
		Steps: make(map[string]CassetteStep),
	}

	// Wrap gz in a counting reader so we can detect bomb expansion.
	limited := &countingReader{src: gz, limit: hard + 1}
	tr := tar.NewReader(limited)

	stepBlobs := make(map[string]map[string][]byte) // step ID → file basename → bytes

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cas.Close()
			return nil, fmt.Errorf("%s: tar: %w", CodeCassetteMalformed, err)
		}

		// Strict entry-name hygiene: clean, reject absolute/traversal.
		name := hdr.Name
		if name == "" {
			continue
		}
		cleaned := filepath.ToSlash(filepath.Clean(name))
		if cleaned == "." || cleaned == "/" {
			continue
		}
		if strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
			_ = cas.Close()
			return nil, fmt.Errorf("%s: tar entry outside root: %q", CodeCassetteMalformed, name)
		}

		// Resolve destination + sanity-check it stays under root.
		dst := filepath.Join(root, filepath.FromSlash(cleaned))
		absRoot, _ := filepath.Abs(root)
		absDst, _ := filepath.Abs(dst)
		if !strings.HasPrefix(absDst, absRoot+string(os.PathSeparator)) && absDst != absRoot {
			_ = cas.Close()
			return nil, fmt.Errorf("%s: tar entry resolves outside root", CodeCassetteMalformed)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				_ = cas.Close()
				return nil, fmt.Errorf("%s: mkdir entry: %w", CodeSvcInternal, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				_ = cas.Close()
				return nil, fmt.Errorf("%s: mkdir parent: %w", CodeSvcInternal, err)
			}
			f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				_ = cas.Close()
				return nil, fmt.Errorf("%s: open: %w", CodeSvcInternal, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				_ = cas.Close()
				if errors.Is(err, errSizeExceeded) {
					return nil, fmt.Errorf("%s: uncompressed exceeds %d bytes", CodeCassetteGzipBomb, hard)
				}
				return nil, fmt.Errorf("%s: write: %w", CodeCassetteMalformed, err)
			}
			if err := f.Close(); err != nil {
				_ = cas.Close()
				return nil, fmt.Errorf("%s: close: %w", CodeSvcInternal, err)
			}

			// Track step blobs for later assembly.
			if strings.HasPrefix(cleaned, "steps/") {
				rest := strings.TrimPrefix(cleaned, "steps/")
				parts := strings.SplitN(rest, "/", 2)
				if len(parts) == 2 {
					id := parts[0]
					base := parts[1]
					if stepBlobs[id] == nil {
						stepBlobs[id] = make(map[string][]byte)
					}
					// We don't keep file body in memory for cassette/* — only for
					// result.json, stdout.txt, stderr.txt.
					if !strings.HasPrefix(base, "cassette/") {
						b, rerr := os.ReadFile(dst)
						if rerr == nil {
							stepBlobs[id][base] = b
						}
					}
				}
			}
		default:
			// Symlinks, devices, etc. are rejected outright.
			_ = cas.Close()
			return nil, fmt.Errorf("%s: unsupported tar entry type %d", CodeCassetteMalformed, hdr.Typeflag)
		}
	}

	cas.UncompressedSz = limited.n

	// manifest.yaml required.
	manifestPath := filepath.Join(root, "manifest.yaml")
	mf, err := os.Open(manifestPath)
	if err != nil {
		_ = cas.Close()
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: manifest.yaml missing", CodeCassetteMalformed)
		}
		return nil, fmt.Errorf("%s: open manifest: %w", CodeSvcInternal, err)
	}
	manifest, perr := LoadManifest(mf)
	_ = mf.Close()
	if perr != nil {
		_ = cas.Close()
		return nil, fmt.Errorf("%s: %w", CodeCassetteManifestInvalid, perr)
	}
	if verr := ValidateManifest(manifest); verr != nil {
		_ = cas.Close()
		return nil, fmt.Errorf("%s: %w", CodeCassetteManifestInvalid, verr)
	}
	cas.Manifest = manifest

	// story.yaml required.
	storyPath := filepath.Join(root, "story.yaml")
	storyBytes, err := os.ReadFile(storyPath)
	if err != nil {
		_ = cas.Close()
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: story.yaml missing", CodeCassetteMalformed)
		}
		return nil, fmt.Errorf("%s: read story: %w", CodeSvcInternal, err)
	}
	sum := sha256.Sum256(storyBytes)
	cas.StoryHash = hex.EncodeToString(sum[:])
	cas.StoryBytes = storyBytes

	// Story hash check.
	expected := strings.TrimPrefix(manifest.StoryRef.ContentHash, "sha256:")
	if !strings.EqualFold(expected, cas.StoryHash) {
		_ = cas.Close()
		return nil, fmt.Errorf("%s: story hash mismatch (manifest=%s actual=sha256:%s)",
			CodeStoryHashMismatch, manifest.StoryRef.ContentHash, cas.StoryHash)
	}

	// Assemble step captures.
	for _, ms := range manifest.Steps {
		blob := stepBlobs[ms.ID]
		raw := blob["result.json"]
		if raw == nil {
			_ = cas.Close()
			return nil, fmt.Errorf("%s: step %q result.json missing", CodeCassetteMalformed, ms.ID)
		}
		var sr stepResultRaw
		if err := json.Unmarshal(raw, &sr); err != nil {
			_ = cas.Close()
			return nil, fmt.Errorf("%s: step %q result.json: %w", CodeCassetteMalformed, ms.ID, err)
		}
		cas.Steps[ms.ID] = CassetteStep{
			ID:          ms.ID,
			CassetteDir: filepath.Join(root, filepath.FromSlash(ms.CassetteDir)),
			ExitCode:    sr.ExitCode,
			DurationMS:  sr.DurationMS,
			Stdout:      blob["stdout.txt"],
			Stderr:      blob["stderr.txt"],
		}
	}

	return cas, nil
}

// ReceiveHTTP wraps Receive with HTTP-side guards: it checks Content-
// Type, applies http.MaxBytesReader to bound the compressed body, and
// translates a few errors into the right *output.Error. The error
// returned carries the symbolic code as a prefix so the caller can
// reshape via WriteError.
func (rc *CassetteReceiver) ReceiveHTTP(w http.ResponseWriter, r *http.Request) (*Cassette, error) {
	hard, _, _ := rc.resolved()
	if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, CassetteContentType) {
		return nil, fmt.Errorf("%s: expected %s, got %q", CodeAcceptUnsupported, CassetteContentType, got)
	}
	// Cap compressed body at hard cap * 2 to bound network input
	// independently of decompression.
	body := http.MaxBytesReader(w, r.Body, hard*2)
	defer body.Close()
	return rc.Receive(body)
}

// countingReader wraps an io.Reader and counts bytes read, capping at
// limit. Returns errSizeExceeded when crossed.
type countingReader struct {
	src   io.Reader
	limit int64
	n     int64
}

var errSizeExceeded = errors.New("cassette: size exceeded")

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n >= c.limit {
		return 0, errSizeExceeded
	}
	max := c.limit - c.n
	if int64(len(p)) > max {
		p = p[:max]
	}
	n, err := c.src.Read(p)
	c.n += int64(n)
	if c.n > c.limit-1 {
		return n, errSizeExceeded
	}
	return n, err
}

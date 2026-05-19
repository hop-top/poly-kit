// Package telemetry — installation_id implementation.
//
// Persists an anonymous installation identifier as 32 raw bytes from
// crypto/rand at <XDG_STATE_HOME>/kit/telemetry/installation_id. The
// surface API returns the lowercase hex SHA-256 of those bytes (64
// chars), so the bytes-on-disk format stays canonical across polyglot
// SDKs (Go, Python, TS, Rust, PHP) while the hashed string is what
// flows through events.
package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"hop.top/kit/go/core/xdg"
)

const (
	// installIDSize is the on-disk byte length: 32 raw bytes from crypto/rand.
	// Hashing 32 bytes with SHA-256 yields a 64-char lowercase hex string.
	installIDSize = 32

	// xdgTool is the tool name used when resolving xdg.StateFile. Fixed to
	// "kit" — polyglot SDKs share the same file, per-tool interpolation
	// would break SDK reads.
	xdgTool = "kit"

	// installIDRel is the relative path under <XDG_STATE_HOME>/<xdgTool>.
	installIDRel = "telemetry/installation_id"

	// installIDFilePerm and installIDDirPerm are the on-disk perms:
	// 0600 for the file, 0700 for the parent dir.
	installIDFilePerm fs.FileMode = 0o600
	installIDDirPerm  fs.FileMode = 0o700
)

// InstallationID returns the persisted anonymous install identifier as
// lowercase hex SHA-256 of 32 random bytes stored on disk. It generates
// and persists the bytes on first call. Concurrent first calls from
// multiple processes are race-safe: the first writer wins via O_EXCL,
// losers re-read.
func InstallationID() (string, error) {
	path, err := InstallIDPath()
	if err != nil {
		return "", err
	}

	// Fast path: file already exists.
	if b, err := readInstallID(path); err == nil {
		return hashHex(b), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	// Slow path: generate + write atomically, tolerating a concurrent
	// first-run from another process.
	buf := make([]byte, installIDSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("install_id: read random: %w", err)
	}

	if err := writeInstallIDExcl(path, buf); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		// Another process won the race; re-read its bytes.
		b, rerr := readInstallID(path)
		if rerr != nil {
			return "", rerr
		}
		return hashHex(b), nil
	}
	return hashHex(buf), nil
}

// Rotate atomically replaces the persisted bytes with 32 fresh
// crypto/rand bytes and returns the new hex. Used by the
// `kit consent reset` CLI (kit-consent track).
func Rotate() (string, error) {
	path, err := InstallIDPath()
	if err != nil {
		return "", err
	}
	if err := ensureParent(path); err != nil {
		return "", err
	}

	buf := make([]byte, installIDSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("install_id: read random: %w", err)
	}

	if err := atomicWrite(path, buf); err != nil {
		return "", err
	}
	return hashHex(buf), nil
}

// ResetForTest deletes the persisted file. Test helper exposed to
// adopters so they can drive first-run code paths in their own tests.
func ResetForTest() error {
	path, err := InstallIDPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("install_id: remove: %w", err)
	}
	// Also clean up any stale *.new from a crashed writer.
	_ = os.Remove(path + ".new")
	return nil
}

// pathMu guards concurrent calls into xdg.StateFile. The underlying
// adrg/xdg package mutates package-level state inside Reload(); without
// this mutex two goroutines calling InstallationID() simultaneously
// race on those globals (-race flags it). The mutex is contended only
// during the StateFile resolve itself, which is microseconds.
var pathMu sync.Mutex

// InstallIDPath returns the canonical on-disk path used by this
// package. Useful for SDKs and compliance checks that need to verify
// the storage location without invoking the read path.
func InstallIDPath() (string, error) {
	pathMu.Lock()
	defer pathMu.Unlock()
	// xdg.StateFile creates parent directories on demand (per its docs)
	// with the underlying lib's default perms. We re-enforce 0700 on the
	// parent dir during writes via ensureParent.
	path, err := xdg.StateFile(xdgTool, installIDRel)
	if err != nil {
		return "", fmt.Errorf("install_id: resolve path: %w", err)
	}
	return path, nil
}

// readInstallID reads the persisted bytes, validating size. Returns
// an fs.ErrNotExist-wrapped error if the file is missing.
func readInstallID(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != installIDSize {
		return nil, fmt.Errorf(
			"install_id: file %q has wrong size %d bytes, expected %d",
			path, len(b), installIDSize,
		)
	}
	return b, nil
}

// writeInstallIDExcl writes buf to a per-call tmp file then publishes
// it via os.Link, which fails with fs.ErrExist if another writer
// already published. This is the "exclusive create" without the
// empty-file race: observers either see no file or a fully-populated
// 32-byte file — never an empty zero-byte target.
//
// The tmp file lives in the same directory so the link is atomic
// (POSIX requires same-filesystem). On link success we unlink the
// tmp; on link failure (EEXIST: another writer won), we still unlink
// the tmp and surface fs.ErrExist.
func writeInstallIDExcl(path string, buf []byte) error {
	if err := ensureParent(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Per-call unique tmp suffix so two concurrent writers don't
	// collide on the tmp file itself.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("install_id: tmp nonce: %w", err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s.tmp.%d.%s", base, os.Getpid(), hex.EncodeToString(nonce[:])))

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, installIDFilePerm)
	if err != nil {
		return fmt.Errorf("install_id: open tmp: %w", err)
	}
	if _, werr := f.Write(buf); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("install_id: write tmp: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install_id: close tmp: %w", cerr)
	}

	// os.Link returns EEXIST when the target already exists, giving us
	// exclusive-publish semantics without ever exposing an empty target.
	if err := os.Link(tmp, path); err != nil {
		_ = os.Remove(tmp)
		// Wrap so errors.Is(err, fs.ErrExist) still matches when another
		// writer won the race.
		return err
	}
	_ = os.Remove(tmp)
	return nil
}

// atomicWrite writes buf to path+".new" then renames over path. Used
// by Rotate so an interrupted rotation can never leave a half-written
// installation_id behind.
func atomicWrite(path string, buf []byte) error {
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, installIDFilePerm)
	if err != nil {
		// If a previous crash left .new behind, remove and retry once.
		if errors.Is(err, fs.ErrExist) {
			if rmErr := os.Remove(tmp); rmErr != nil {
				return fmt.Errorf("install_id: stale tmp: %w", rmErr)
			}
			f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, installIDFilePerm)
		}
		if err != nil {
			return fmt.Errorf("install_id: open tmp: %w", err)
		}
	}
	if _, werr := f.Write(buf); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("install_id: write tmp: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install_id: close tmp: %w", cerr)
	}
	if rerr := os.Rename(tmp, path); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install_id: rename: %w", rerr)
	}
	return nil
}

// ensureParent creates the parent directory of path with 0700 (idempotent).
func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, installIDDirPerm); err != nil {
		return fmt.Errorf("install_id: mkdir parent: %w", err)
	}
	// MkdirAll respects existing perms; force-tighten in case xdg's
	// pre-created parent landed at 0750.
	if err := os.Chmod(dir, installIDDirPerm); err != nil {
		return fmt.Errorf("install_id: chmod parent: %w", err)
	}
	return nil
}

// hashHex returns the lowercase hex SHA-256 of buf.
func hashHex(buf []byte) string {
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

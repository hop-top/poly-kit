package conformance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance"
)

// runCmdSplit executes the conformance command with stdout and stderr
// captured separately (runCmd merges them, which hides warnings).
func runCmdSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := conformance.Cmd()
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// vnlJSON mirrors the wire shape of the verify-no-leak JSON report;
// only the fields these tests assert on.
type vnlJSON struct {
	ScannedFiles int `json:"scanned_files"`
	Findings     []struct {
		File string `json:"file"`
		Rule string `json:"rule"`
	} `json:"findings"`
	Skipped []struct {
		File   string `json:"file"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
}

const cleanYAML = "name: my-tool\nversion: 1\n"

const leakYAML = `scenario_id: paths.recursion.check
assertions:
  - kind: exit_code_equals
  - kind: cassette_must_not_contain
`

// TestVerifyNoLeak_PathsDirectoryRecursion pins the fail-open fix: a
// directory entry in --paths must recurse and actually scan the
// supported files inside it, instead of landing in skipped with
// "unsupported extension" and a vacuous scanned_files=0 pass.
func TestVerifyNoLeak_PathsDirectoryRecursion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		paths       func(root string) []string
		wantScanned int
		wantSkipped int
		wantLeak    bool
	}{
		{
			name: "dir with supported files scanned",
			files: map[string]string{
				"d/a.yaml": cleanYAML,
				"d/b.md":   "# readme\n",
				"d/c.go":   "package d\n",
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "d")} },
			wantScanned: 2,
		},
		{
			name: "nested dirs scanned",
			files: map[string]string{
				"d/sub/deep/x.yml": cleanYAML,
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "d")} },
			wantScanned: 1,
		},
		{
			name: "dot-dir skipped",
			files: map[string]string{
				"d/.hidden/leak.yaml": leakYAML,
				"d/ok.yaml":           cleanYAML,
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "d")} },
			wantScanned: 1,
		},
		{
			name: "mixed contents collect only supported",
			files: map[string]string{
				"d/a.yaml": cleanYAML,
				"d/b.txt":  "plain text\n",
				"d/noext":  "raw\n",
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "d")} },
			wantScanned: 1,
			wantSkipped: 0, // unsupported files in a recursed dir are not report noise
		},
		{
			name: "explicit unsupported file still reported skipped",
			files: map[string]string{
				"main.go": "package main\n",
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "main.go")} },
			wantScanned: 0,
			wantSkipped: 1,
		},
		{
			name: "dedup file passed directly and via its dir",
			files: map[string]string{
				"d/a.yaml": cleanYAML,
				"d/b.yaml": cleanYAML,
			},
			paths: func(root string) []string {
				return []string{filepath.Join(root, "d"), filepath.Join(root, "d", "a.yaml")}
			},
			wantScanned: 2,
		},
		{
			name: "leak inside recursed dir detected",
			files: map[string]string{
				"d/scenarios/launch.yaml": leakYAML,
			},
			paths:       func(root string) []string { return []string{filepath.Join(root, "d")} },
			wantScanned: 1,
			wantLeak:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, tc.files)
			args := []string{"verify-no-leak", "--format", "json", "--paths"}
			for i, p := range tc.paths(root) {
				if i == 0 {
					args = append(args, p)
				} else {
					args = append(args, "--paths", p)
				}
			}
			stdout, _, err := runCmdSplit(t, args...)
			if tc.wantLeak {
				require.Error(t, err)
				assert.True(t, errors.Is(err, conformance.ErrLeakDetected),
					"leak in a recursed dir must exit with the leak class")
			} else {
				require.NoError(t, err)
			}
			// Decode only the first JSON value: on error exits the bare
			// cobra tree appends usage text after the report.
			var rep vnlJSON
			dec := json.NewDecoder(strings.NewReader(stdout))
			require.NoError(t, dec.Decode(&rep), "stdout: %s", stdout)
			assert.Equal(t, tc.wantScanned, rep.ScannedFiles, "scanned_files")
			assert.Len(t, rep.Skipped, tc.wantSkipped, "skipped entries")
			if tc.wantLeak {
				assert.NotEmpty(t, rep.Findings, "leak file must produce findings")
			}
			if tc.name == "explicit unsupported file still reported skipped" {
				require.Len(t, rep.Skipped, 1)
				assert.Equal(t, "unsupported extension", rep.Skipped[0].Reason)
			}
		})
	}
}

// TestVerifyNoLeak_PathsZeroScannedWarns pins the additive stderr
// warning: a --paths invocation that scanned nothing still exits 0
// (an all-Go tree is legitimately clean) but must say so on stderr,
// even under --quiet-on-clean.
func TestVerifyNoLeak_PathsZeroScannedWarns(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"d/only.go": "package d\n"})
	dir := filepath.Join(root, "d")

	stdout, stderr, err := runCmdSplit(t, "verify-no-leak", "--format", "json", "--paths", dir)
	require.NoError(t, err, "0 files scanned keeps exit 0")
	assert.Contains(t, stderr, "0 files scanned", "vacuous pass must warn on stderr")
	var rep vnlJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &rep))
	assert.Equal(t, 0, rep.ScannedFiles)

	// --quiet-on-clean suppresses the report, not the warning.
	stdout, stderr, err = runCmdSplit(t, "verify-no-leak", "--quiet-on-clean", "--paths", dir)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "0 files scanned")
}

// TestVerifyNoLeak_PathsScannedWithFilesDoesNotWarn guards the warning
// against false positives: any scanned file silences it.
func TestVerifyNoLeak_PathsScannedWithFilesDoesNotWarn(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"d/a.yaml": cleanYAML})

	_, stderr, err := runCmdSplit(t, "verify-no-leak", "--format", "json", "--paths", filepath.Join(root, "d"))
	require.NoError(t, err)
	assert.NotContains(t, stderr, "0 files scanned")
}

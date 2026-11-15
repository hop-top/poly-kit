//go:build parity

package parity_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type startupInfo struct {
	Port          int    `json:"port"`
	PID           int    `json:"pid"`
	Token         string `json:"token"`
	ShutdownToken string `json:"shutdown_token"`
}

func TestEngineSDKParityAgainstKitServe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "kit-test")
	tsOutDir := filepath.Join(t.TempDir(), "ts-dist")
	runCmd(t, ctx, root, "go", "build", "-mod=mod", "-buildvcs=false", "-o", bin, "./cmd/kit/")
	runCmd(t, ctx, root,
		filepath.Join(root, "engine/sdk/ts-kit-engine/node_modules/.bin/tsc"),
		"--module", "commonjs",
		"--target", "ES2020",
		"--outDir", tsOutDir,
		"--rootDir", filepath.Join(root, "engine/sdk/ts-kit-engine/src"),
		"--esModuleInterop",
		"--skipLibCheck",
		"--types", "node",
		"--typeRoots", filepath.Join(root, "engine/sdk/ts-kit-engine/node_modules/@types"),
		filepath.Join(root, "engine/sdk/ts-kit-engine/src/index.ts"),
	)

	info, stop := startKitServe(t, ctx, root, bin)
	defer stop()

	runCmd(t, ctx, filepath.Join(root, "engine/sdk/py-kit-engine"), "uv", "sync", "--all-extras", "-q")
	runCmd(t, ctx, filepath.Join(root, "engine/sdk/php-kit-engine"),
		"composer", "install", "--no-interaction", "--no-progress", "--prefer-dist")
	rsTarget := t.TempDir()
	runCmdEnv(t, ctx, filepath.Join(root, "engine/sdk/rs-kit-engine"),
		[]string{"CARGO_TARGET_DIR=" + rsTarget},
		"cargo", "build", "--features", "parity", "--bin", "parity_runner")

	tsScript := writeScript(t, "sdk-parity-*.js", tsParityScript(filepath.Join(tsOutDir, "index.js")))
	pyScript := writeScript(t, "sdk-parity-*.py", pyParityScript(root))

	runners := []sdkRunner{
		{"ts", root, []string{"node", tsScript}},
		{"py", filepath.Join(root, "engine/sdk/py-kit-engine"), []string{"uv", "run", "python", pyScript}},
		{"php", filepath.Join(root, "engine/sdk/php-kit-engine"), []string{"php", "tests/parity_runner.php"}},
		{"rs", filepath.Join(root, "engine/sdk/rs-kit-engine"), []string{filepath.Join(rsTarget, "debug", "parity_runner")}},
	}
	payloads := map[string]map[string]any{}
	for _, r := range runners {
		out := runSDKScript(t, ctx, r.dir, r.argv, info)
		var p map[string]any
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatalf("decode %s payload: %v\n%s", r.name, err, out)
		}
		payloads[r.name] = p
	}
	if diffs := diffPayloads(runners, payloads); len(diffs) > 0 {
		t.Fatalf("SDK parity mismatch (baseline %s):\n%s", runners[0].name, strings.Join(diffs, "\n"))
	}
}

type sdkRunner struct {
	name string
	dir  string
	argv []string
}

// diffPayloads compares every runner against the first (baseline) and
// returns one line per drifting surface naming the SDK and key.
func diffPayloads(runners []sdkRunner, payloads map[string]map[string]any) []string {
	base := runners[0].name
	var diffs []string
	for _, r := range runners[1:] {
		keys := map[string]bool{}
		for k := range payloads[base] {
			keys[k] = true
		}
		for k := range payloads[r.name] {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			want, got := payloads[base][k], payloads[r.name][k]
			if !reflect.DeepEqual(want, got) {
				diffs = append(diffs, fmt.Sprintf("  surface %q: %s != %s\n    - %s: %s\n    + %s: %s",
					k, r.name, base, base, mustJSON(want), r.name, mustJSON(got)))
			}
		}
	}
	return diffs
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "../../.."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runCmd(t *testing.T, ctx context.Context, dir string, name string, args ...string) {
	t.Helper()
	runCmdEnv(t, ctx, dir, nil, name, args...)
}

func runCmdEnv(t *testing.T, ctx context.Context, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func startKitServe(t *testing.T, ctx context.Context, root, bin string) (startupInfo, func()) {
	t.Helper()
	dataDir := t.TempDir()
	cmd := exec.CommandContext(ctx, bin, "serve", "--port", "0", "--data", dataDir, "--no-peer", "--no-sync")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		_ = cmd.Process.Kill()
		t.Fatalf("kit serve produced no startup JSON: %s", stderr.String())
	}
	var info startupInfo
	if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("parse startup JSON: %v (%q)", err, scanner.Text())
	}

	stop := func() {
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/shutdown", info.Port), nil)
		req.Header.Set("Authorization", "Bearer "+info.ShutdownToken)
		_, _ = http.DefaultClient.Do(req)
		_ = cmd.Wait()
	}
	return info, stop
}

func writeScript(t *testing.T, pattern, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), pattern)
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func runSDKScript(t *testing.T, ctx context.Context, dir string, argv []string, info startupInfo) []byte {
	t.Helper()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KIT_ENGINE_PORT=%d", info.Port),
		"KIT_ENGINE_TOKEN="+info.Token,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", argv, err, out)
	}
	return bytes.TrimSpace(out)
}

func tsParityScript(modulePath string) string {
	return fmt.Sprintf(`
const { KitEngine } = require(%q);

(async () => {
  const engine = await KitEngine.connect(Number(process.env.KIT_ENGINE_PORT), process.env.KIT_ENGINE_TOKEN);
  const notes = engine.collection("notes");
  const created = await notes.create({ title: "v1" });
  const id = created.id;
  await notes.update(id, { title: "v2" });
  await notes.update(id, { title: "v3" });
  const before = await notes.history(id);
  const reverted = await notes.revert(id, 1);
  const after = await notes.history(id);
  // Branch ops on their own doc; errors normalize to the HTTP status.
  const status = async (fn) => {
    try { await fn(); return 0; } catch (e) { return Number(/: (\d+)$/.exec(e.message)[1]); }
  };
  const seqs = (hs) => hs.map(h => h.seq).sort((a, b) => a - b);
  const b = (await notes.create({ title: "b1" })).id;
  await notes.update(b, { title: "b2" });
  await notes.update(b, { title: "b3" });
  const forkA = await notes.fork(b, 2);
  const headsAfterFork = seqs(await notes.branches(b));
  await notes.abandon(b, forkA.seq);
  const liveAfterAbandon = seqs(await notes.branches(b, { live: true }));
  const abandonLastLive = await status(() => notes.abandon(b, 3));
  const forkB = await notes.fork(b, 2);
  const merged = await notes.merge(b, forkB.seq, 3, { title: "merged" });
  const pruneNoPolicy = await status(() => notes.prune(b, {}));
  const pruned = await notes.prune(b, { max_versions: 1 });
  const missing = "no-such-doc";
  console.log(JSON.stringify({
    forkSeq: forkA.seq,
    forkParents: forkA.parent_ids.length,
    forkKeys: Object.keys(forkA).sort(),
    headsAfterFork,
    liveAfterAbandon,
    abandonLastLive,
    forkBSeq: forkB.seq,
    mergeSeq: merged.seq,
    mergeParents: merged.parent_ids.length,
    pruneNoPolicy,
    pruneRemoved: pruned.versions_removed.length,
    pruneBlobsFreed: pruned.blobs_freed,
    pruneBytesFreed: pruned.bytes_freed,
    forkOutOfRange: await status(() => notes.fork(b, 99)),
    mergeOutOfRange: await status(() => notes.merge(b, 99, 1, { title: "x" })),
    forkMissing: await status(() => notes.fork(missing, 1)),
    mergeMissing: await status(() => notes.merge(missing, 2, 1, { title: "x" })),
    pruneMissing: await status(() => notes.prune(missing, { max_versions: 1 })),
    abandonMissing: await status(() => notes.abandon(missing, 1)),
    createdKeys: Object.keys(created).sort(),
    beforeVersions: before.map(v => v.version),
    beforeOps: before.map(v => v.operation),
    beforeTitles: before.map(v => v.data.title),
    revertedTitle: reverted.data.title,
    afterVersions: after.map(v => v.version),
    afterOps: after.map(v => v.operation),
    afterTitles: after.map(v => v.data.title),
  }));
})().catch((err) => { console.error(err); process.exit(1); });
`, modulePath)
}

func pyParityScript(root string) string {
	return fmt.Sprintf(`
import json
import os
import sys

import requests

sys.path.insert(0, %q)

from kit_engine import KitEngine

engine = KitEngine.connect(int(os.environ["KIT_ENGINE_PORT"]), os.environ["KIT_ENGINE_TOKEN"])
notes = engine.collection("notes")
created = notes.create({"title": "v1"})
doc_id = created["id"]
notes.update(doc_id, {"title": "v2"})
notes.update(doc_id, {"title": "v3"})
before = notes.history(doc_id)
reverted = notes.revert(doc_id, 1)
after = notes.history(doc_id)


def status(fn):
    try:
        fn()
        return 0
    except requests.HTTPError as e:
        return e.response.status_code


def seqs(heads):
    return sorted(h["seq"] for h in heads)


b = notes.create({"title": "b1"})["id"]
notes.update(b, {"title": "b2"})
notes.update(b, {"title": "b3"})
fork_a = notes.fork(b, 2)
heads_after_fork = seqs(notes.branches(b))
notes.abandon(b, fork_a["seq"])
live_after_abandon = seqs(notes.branches(b, live=True))
abandon_last_live = status(lambda: notes.abandon(b, 3))
fork_b = notes.fork(b, 2)
merged = notes.merge(b, fork_b["seq"], 3, {"title": "merged"})
prune_no_policy = status(lambda: notes.prune(b))
pruned = notes.prune(b, max_versions=1)
missing = "no-such-doc"
print(json.dumps({
    "forkSeq": fork_a["seq"],
    "forkParents": len(fork_a["parent_ids"]),
    "forkKeys": sorted(fork_a.keys()),
    "headsAfterFork": heads_after_fork,
    "liveAfterAbandon": live_after_abandon,
    "abandonLastLive": abandon_last_live,
    "forkBSeq": fork_b["seq"],
    "mergeSeq": merged["seq"],
    "mergeParents": len(merged["parent_ids"]),
    "pruneNoPolicy": prune_no_policy,
    "pruneRemoved": len(pruned["versions_removed"]),
    "pruneBlobsFreed": pruned["blobs_freed"],
    "pruneBytesFreed": pruned["bytes_freed"],
    "forkOutOfRange": status(lambda: notes.fork(b, 99)),
    "mergeOutOfRange": status(lambda: notes.merge(b, 99, 1, {"title": "x"})),
    "forkMissing": status(lambda: notes.fork(missing, 1)),
    "mergeMissing": status(lambda: notes.merge(missing, 2, 1, {"title": "x"})),
    "pruneMissing": status(lambda: notes.prune(missing, max_versions=1)),
    "abandonMissing": status(lambda: notes.abandon(missing, 1)),
    "createdKeys": sorted(created.keys()),
    "beforeVersions": [v["version"] for v in before],
    "beforeOps": [v["operation"] for v in before],
    "beforeTitles": [v["data"]["title"] for v in before],
    "revertedTitle": reverted["data"]["title"],
    "afterVersions": [v["version"] for v in after],
    "afterOps": [v["operation"] for v in after],
    "afterTitles": [v["data"]["title"] for v in after],
}, separators=(",", ":")))
`, filepath.Join(root, "engine/sdk/py-kit-engine"))
}

func TestDiffPayloadsNamesDriftingSurface(t *testing.T) {
	runners := []sdkRunner{{name: "ts"}, {name: "py"}, {name: "php"}, {name: "rs"}}
	same := map[string]any{"afterOps": []any{"update"}, "revertedTitle": "v1"}
	drift := map[string]any{"afterOps": []any{"update"}, "revertedTitle": "v2"}
	payloads := map[string]map[string]any{"ts": same, "py": same, "php": same, "rs": drift}

	diffs := diffPayloads(runners, payloads)
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d: %v", len(diffs), diffs)
	}
	for _, want := range []string{`"revertedTitle"`, "rs", "ts", `"v2"`, `"v1"`} {
		if !strings.Contains(diffs[0], want) {
			t.Errorf("diff %q missing %q", diffs[0], want)
		}
	}
	payloads["rs"] = same
	if d := diffPayloads(runners, payloads); len(d) != 0 {
		t.Fatalf("want no diffs, got %v", d)
	}
}

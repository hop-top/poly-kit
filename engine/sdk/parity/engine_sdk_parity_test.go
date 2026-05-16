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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	tsScript := writeScript(t, "sdk-parity-*.js", tsParityScript(filepath.Join(tsOutDir, "index.js")))
	pyScript := writeScript(t, "sdk-parity-*.py", pyParityScript(root))

	tsOut := runSDKScript(t, ctx, root, []string{"node", tsScript}, info)
	pyOut := runSDKScript(t, ctx, filepath.Join(root, "engine/sdk/py-kit-engine"), []string{"uv", "run", "python", pyScript}, info)

	var tsPayload map[string]any
	if err := json.Unmarshal(tsOut, &tsPayload); err != nil {
		t.Fatalf("decode ts payload: %v\n%s", err, tsOut)
	}
	var pyPayload map[string]any
	if err := json.Unmarshal(pyOut, &pyPayload); err != nil {
		t.Fatalf("decode py payload: %v\n%s", err, pyOut)
	}
	if !reflect.DeepEqual(tsPayload, pyPayload) {
		t.Fatalf("SDK parity mismatch\nTS: %s\nPY: %s", tsOut, pyOut)
	}
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
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
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
  console.log(JSON.stringify({
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
print(json.dumps({
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

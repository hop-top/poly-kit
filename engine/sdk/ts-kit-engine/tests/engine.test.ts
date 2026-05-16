import { describe, it, expect, vi, beforeEach } from "vitest";
import { EventEmitter } from "events";

// Mock child_process before importing
vi.mock("child_process", () => ({
  spawn: vi.fn(),
}));

import { KitEngine } from "../src/index";
import { spawn } from "child_process";

function createMockProcess(startupJSON: string) {
  const stdout = new EventEmitter();
  const proc = new EventEmitter() as any;
  proc.stdout = stdout;
  proc.stdin = null;
  proc.stderr = null;

  // emit startup line async
  setTimeout(() => stdout.emit("data", Buffer.from(startupJSON + "\n")), 5);
  return proc;
}

describe("KitEngine", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("start spawns kit serve and parses startup JSON", async () => {
    const mockProc = createMockProcess('{"port":9876,"pid":1234}');
    (spawn as any).mockReturnValue(mockProc);

    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true }));

    const engine = await KitEngine.start({ app: "test", port: 0 });

    expect(engine.port).toBe(9876);
    expect(engine.pid).toBe(1234);
    expect(spawn).toHaveBeenCalledWith(
      "kit",
      expect.arrayContaining(["serve", "--port", "0", "--app", "test"]),
      expect.anything(),
    );
  });

  it("start passes flags correctly", async () => {
    const mockProc = createMockProcess('{"port":5555,"pid":99}');
    (spawn as any).mockReturnValue(mockProc);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true }));

    await KitEngine.start({
      binPath: "/usr/local/bin/kit",
      encrypt: true,
      noPeer: true,
      noSync: true,
    });

    expect(spawn).toHaveBeenCalledWith(
      "/usr/local/bin/kit",
      expect.arrayContaining(["--encrypt", "--no-peer", "--no-sync"]),
      expect.anything(),
    );
  });

  it("connect verifies health and returns instance", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ pid: 42 }),
      }),
    );

    const engine = await KitEngine.connect(8080);

    expect(engine.port).toBe(8080);
    expect(engine.pid).toBe(42);
  });

  it("connect throws on failed health check", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 503 }),
    );

    await expect(KitEngine.connect(8080)).rejects.toThrow("health check failed");
  });

  it("stop sends POST /shutdown", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ pid: 1 }),
      }),
    );

    const engine = await KitEngine.connect(9000);
    await engine.stop();

    expect(fetch).toHaveBeenCalledWith(
      "http://127.0.0.1:9000/shutdown",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("collection returns a Collection instance", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ pid: 1 }),
      }),
    );

    const engine = await KitEngine.connect(7000);
    const col = engine.collection("notes");

    expect(col).toBeDefined();
  });
});

import { describe, it, expect, vi, afterEach } from "vitest";
import {
  parseTritonURI,
  TritonClient,
  TritonError,
} from "./client";
import type { InferResponse } from "./client";

// ─── parseTritonURI ──────────────────────────────────────────────────────────

describe("parseTritonURI", () => {
  it("parses host:port/model", () => {
    const r = parseTritonURI("triton://localhost:8000/my_model");
    expect(r.host).toBe("localhost");
    expect(r.port).toBe(8000);
    expect(r.modelName).toBe("my_model");
    expect(r.modelVersion).toBeUndefined();
  });

  it("parses host:port/model/version", () => {
    const r = parseTritonURI("triton://gpu:9000/bert/1");
    expect(r.host).toBe("gpu");
    expect(r.port).toBe(9000);
    expect(r.modelName).toBe("bert");
    expect(r.modelVersion).toBe("1");
  });

  it("throws on wrong scheme", () => {
    expect(() => parseTritonURI("http://x:8000/m")).toThrow(TritonError);
  });

  it("throws on missing model", () => {
    expect(() => parseTritonURI("triton://x:8000/")).toThrow(TritonError);
  });

  it("throws on missing port", () => {
    expect(() => parseTritonURI("triton://host/model")).toThrow(TritonError);
  });

  it("throws on non-numeric port", () => {
    expect(() => parseTritonURI("triton://host:abc/model")).toThrow(
      TritonError,
    );
  });
});

// ─── TritonClient ────────────────────────────────────────────────────────────

describe("TritonClient", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("constructs from URI", () => {
    const c = TritonClient.fromURI("triton://localhost:8000/mf_model");
    expect(c.baseUrl).toBe("http://localhost:8000");
    expect(c.modelName).toBe("mf_model");
  });

  it("infer sends correct request and parses response", async () => {
    const mockResponse: InferResponse = {
      model_name: "test",
      outputs: [
        {
          name: "output",
          shape: [1, 1],
          datatype: "FP32",
          data: [0.75],
        },
      ],
    };

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      json: async () => mockResponse,
      text: async () => "",
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "test");
    const resp = await client.infer({
      inputs: [
        {
          name: "input",
          shape: [1, 4],
          datatype: "FP32",
          data: [1, 2, 3, 4],
        },
      ],
    });

    expect(resp.outputs[0].data[0]).toBe(0.75);
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "http://localhost:8000/v2/models/test/infer",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("infer includes version in URL when set", async () => {
    const mockResponse: InferResponse = {
      model_name: "test",
      outputs: [{ name: "o", shape: [1], datatype: "FP32", data: [0] }],
    };

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      json: async () => mockResponse,
      text: async () => "",
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "test", "2");
    await client.infer({ inputs: [] });

    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock
      .calls[0][0] as string;
    expect(url).toContain("/versions/2/infer");
  });

  it("infer throws TritonError on HTTP error", async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: false,
      status: 500,
      text: async () => "internal error",
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "test");
    await expect(client.infer({ inputs: [] })).rejects.toThrow(TritonError);
  });

  it("score sends FP32 input and returns first output value", async () => {
    const mockResponse: InferResponse = {
      model_name: "mf",
      outputs: [
        {
          name: "output",
          shape: [1, 1],
          datatype: "FP32",
          data: [0.42],
        },
      ],
    };

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      json: async () => mockResponse,
      text: async () => "",
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "mf");
    const result = await client.score(new Float32Array([1, 2, 3]));
    expect(result).toBe(0.42);
  });

  it("score throws on empty output", async () => {
    const mockResponse: InferResponse = {
      model_name: "mf",
      outputs: [],
    };

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      json: async () => mockResponse,
      text: async () => "",
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "mf");
    await expect(
      client.score(new Float32Array([1])),
    ).rejects.toThrow(TritonError);
  });

  it("health returns live/ready status", async () => {
    globalThis.fetch = vi.fn(async (url: string) => {
      if (url.includes("live")) return { ok: true };
      if (url.includes("ready")) return { ok: false };
      return { ok: false };
    }) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "test");
    const h = await client.health();
    expect(h.live).toBe(true);
    expect(h.ready).toBe(false);
  });

  it("health handles fetch errors gracefully", async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new Error("network error");
    }) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "test");
    const h = await client.health();
    expect(h.live).toBe(false);
    expect(h.ready).toBe(false);
  });

  it("modelReady returns boolean", async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
    })) as unknown as typeof fetch;

    const client = new TritonClient("http://localhost:8000", "mf");
    expect(await client.modelReady()).toBe(true);
  });
});

/**
 * Triton Inference Server HTTP+JSON client.
 *
 * Implements the KServe v2 inference protocol over HTTP for calling
 * Triton-hosted models. Supports triton:// URI scheme parsing.
 *
 * @see https://kserve.github.io/website/modelserving/inference_api/
 */

// ─── Types ───────────────────────────────────────────────────────────────────

/** KServe v2 tensor data types used in requests/responses. */
export type TritonDataType =
  | "BOOL"
  | "UINT8"
  | "UINT16"
  | "UINT32"
  | "UINT64"
  | "INT8"
  | "INT16"
  | "INT32"
  | "INT64"
  | "FP16"
  | "FP32"
  | "FP64"
  | "BYTES";

/** A single input tensor for an inference request. */
export interface InferInput {
  name: string;
  shape: number[];
  datatype: TritonDataType;
  data: number[] | string[] | boolean[];
}

/** A requested output tensor. */
export interface InferRequestedOutput {
  name: string;
}

/** KServe v2 inference request body. */
export interface InferRequest {
  inputs: InferInput[];
  outputs?: InferRequestedOutput[];
}

/** A single output tensor in an inference response. */
export interface InferOutputTensor {
  name: string;
  shape: number[];
  datatype: TritonDataType;
  data: number[] | string[] | boolean[];
}

/** KServe v2 inference response body. */
export interface InferResponse {
  model_name: string;
  model_version?: string;
  id?: string;
  outputs: InferOutputTensor[];
}

/** Health status from Triton server. */
export interface ServerHealth {
  live: boolean;
  ready: boolean;
}

/** Parsed triton:// URI. */
export interface TritonURI {
  host: string;
  port: number;
  modelName: string;
  modelVersion?: string;
}

// ─── Errors ──────────────────────────────────────────────────────────────────

export class TritonError extends Error {
  readonly statusCode?: number;

  constructor(message: string, statusCode?: number) {
    super(message);
    this.name = "TritonError";
    this.statusCode = statusCode;
  }
}

// ─── URI parsing ─────────────────────────────────────────────────────────────

/**
 * Parse a triton:// URI.
 *
 * Format: triton://host:port/model_name[/version]
 *
 * Examples:
 *   triton://localhost:8000/my_model
 *   triton://gpu-server:8000/bert/1
 */
export function parseTritonURI(uri: string): TritonURI {
  if (!uri.startsWith("triton://")) {
    throw new TritonError(
      `Invalid Triton URI "${uri}": must start with triton://`,
    );
  }

  const rest = uri.slice("triton://".length);
  const slashIdx = rest.indexOf("/");
  if (slashIdx < 0) {
    throw new TritonError(
      `Invalid Triton URI "${uri}": missing model name`,
    );
  }

  const hostPort = rest.slice(0, slashIdx);
  const path = rest.slice(slashIdx + 1);

  const colonIdx = hostPort.lastIndexOf(":");
  if (colonIdx < 0) {
    throw new TritonError(
      `Invalid Triton URI "${uri}": missing port`,
    );
  }

  const host = hostPort.slice(0, colonIdx);
  if (!host) {
    throw new TritonError(`Invalid Triton URI "${uri}": missing host`);
  }
  const port = Number(hostPort.slice(colonIdx + 1));
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new TritonError(
      `Invalid Triton URI "${uri}": invalid port`,
    );
  }

  const parts = path.split("/").filter(Boolean);
  if (parts.length === 0) {
    throw new TritonError(
      `Invalid Triton URI "${uri}": missing model name`,
    );
  }

  return {
    host,
    port,
    modelName: parts[0],
    modelVersion: parts.length > 1 ? parts[1] : undefined,
  };
}

// ─── TritonScorer interface ──────────────────────────────────────────────────

/** Simplified interface for routers that just need a single score. */
export interface TritonScorer {
  score(input: Float32Array): Promise<number>;
}

// ─── Client ──────────────────────────────────────────────────────────────────

export class TritonClient implements TritonScorer {
  readonly baseUrl: string;
  readonly modelName: string;
  readonly modelVersion?: string;

  constructor(baseUrl: string, modelName: string, version?: string) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.modelName = modelName;
    this.modelVersion = version;
  }

  /**
   * Create a TritonClient from a triton:// URI.
   */
  static fromURI(uri: string): TritonClient {
    const parsed = parseTritonURI(uri);
    const baseUrl = `http://${parsed.host}:${parsed.port}`;
    return new TritonClient(baseUrl, parsed.modelName, parsed.modelVersion);
  }

  /**
   * Send an inference request to Triton.
   */
  async infer(request: InferRequest): Promise<InferResponse> {
    const versionPath = this.modelVersion
      ? `/versions/${this.modelVersion}`
      : "";
    const url =
      `${this.baseUrl}/v2/models/${this.modelName}` +
      `${versionPath}/infer`;

    const resp = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(request),
    });

    if (!resp.ok) {
      const text = await resp.text().catch(() => "");
      throw new TritonError(
        `Triton inference failed: HTTP ${resp.status} — ${text}`,
        resp.status,
      );
    }

    return (await resp.json()) as InferResponse;
  }

  /**
   * Simplified score: send a single FP32 input array, return the
   * first value of the first output tensor.
   */
  async score(input: Float32Array): Promise<number> {
    const request: InferRequest = {
      inputs: [
        {
          name: "input",
          shape: [1, input.length],
          datatype: "FP32",
          data: Array.from(input),
        },
      ],
    };

    const response = await this.infer(request);
    const output = response.outputs[0];
    if (!output || !output.data.length) {
      throw new TritonError("Triton response has no output data");
    }

    return output.data[0] as number;
  }

  /**
   * Check server health (liveness + readiness).
   */
  async health(): Promise<ServerHealth> {
    const [liveResp, readyResp] = await Promise.all([
      fetch(`${this.baseUrl}/v2/health/live`).catch(() => null),
      fetch(`${this.baseUrl}/v2/health/ready`).catch(() => null),
    ]);

    return {
      live: liveResp?.ok ?? false,
      ready: readyResp?.ok ?? false,
    };
  }

  /**
   * Check if a specific model is ready.
   */
  async modelReady(): Promise<boolean> {
    const url =
      `${this.baseUrl}/v2/models/${this.modelName}/ready`;
    try {
      const resp = await fetch(url);
      return resp.ok;
    } catch {
      return false;
    }
  }
}

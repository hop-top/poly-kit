/**
 * Typed REST API client + WebSocket client for kit services.
 *
 * Mirrors Go-side api.Service[T] (CRUD) and api.WSMessage (pub/sub).
 */

// --- Types ---

export interface Entity {
  id: string;
}

export interface Query {
  limit?: number;
  offset?: number;
  sort?: string;
  search?: string;
}

export interface APIError {
  status: number;
  code: string;
  message: string;
}

export interface WSMessage {
  type: string;
  topic: string;
  payload: unknown;
}

// --- APIError as throwable ---

export class APIRequestError extends Error implements APIError {
  status: number;
  code: string;

  constructor(err: APIError) {
    super(err.message);
    this.name = "APIRequestError";
    this.status = err.status;
    this.code = err.code;
  }
}

// --- APIClient ---

export interface APIClientOptions {
  auth?: string;
  fetch?: typeof globalThis.fetch;
}

export class APIClient<T extends Entity> {
  private baseURL: string;
  private auth?: string;
  private fetchFn: typeof globalThis.fetch;

  constructor(baseURL: string, opts?: APIClientOptions) {
    // strip trailing slash
    this.baseURL = baseURL.replace(/\/+$/, "");
    this.auth = opts?.auth;
    this.fetchFn = opts?.fetch ?? globalThis.fetch.bind(globalThis);
  }

  private headers(): Record<string, string> {
    const h: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (this.auth) {
      h["Authorization"] = `Bearer ${this.auth}`;
    }
    return h;
  }

  private async request<R>(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<R> {
    const res = await this.fetchFn(`${this.baseURL}${path}`, {
      method,
      headers: this.headers(),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });

    if (!res.ok) {
      let err: APIError;
      try {
        err = (await res.json()) as APIError;
      } catch {
        err = {
          status: res.status,
          code: "unknown",
          message: res.statusText,
        };
      }
      throw new APIRequestError(err);
    }

    if (res.status === 204) return undefined as R;
    return (await res.json()) as R;
  }

  async create(entity: Omit<T, "id"> | T): Promise<T> {
    return this.request<T>("POST", "/", entity);
  }

  async get(id: string): Promise<T> {
    return this.request<T>("GET", `/${encodeURIComponent(id)}`);
  }

  async list(q?: Query): Promise<T[]> {
    const params = new URLSearchParams();
    if (q?.limit !== undefined) params.set("limit", String(q.limit));
    if (q?.offset !== undefined) params.set("offset", String(q.offset));
    if (q?.sort) params.set("sort", q.sort);
    if (q?.search) params.set("search", q.search);
    const qs = params.toString();
    return this.request<T[]>("GET", `/${qs ? `?${qs}` : ""}`);
  }

  async update(entity: T): Promise<T> {
    return this.request<T>(
      "PUT",
      `/${encodeURIComponent(entity.id)}`,
      entity,
    );
  }

  async delete(id: string): Promise<void> {
    return this.request<void>(
      "DELETE",
      `/${encodeURIComponent(id)}`,
    );
  }
}

// --- WSClient ---

type MessageHandler = (msg: WSMessage) => void;

export class WSClient {
  private url: string;
  private ws?: WebSocket;
  private handlers: MessageHandler[] = [];

  constructor(url: string) {
    this.url = url;
  }

  private static async resolveWS(): Promise<typeof WebSocket> {
    if (typeof globalThis.WebSocket !== "undefined") {
      return globalThis.WebSocket;
    }
    // Node <21 fallback; ws is an optional peer dep.
    // CJS-only — safe because package.json declares "type": "commonjs".
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    try { return require("ws") as typeof WebSocket; } catch { /* noop */ }
    throw new Error("ws: no WebSocket implementation available");
  }

  async connect(): Promise<void> {
    const WS = await WSClient.resolveWS();

    return new Promise<void>((resolve, reject) => {
      const ws = new WS(this.url);

      ws.onopen = () => {
        this.ws = ws;
        resolve();
      };

      ws.onerror = (e: Event) => {
        reject(e);
      };

      ws.onmessage = (ev: MessageEvent) => {
        try {
          const msg = JSON.parse(
            typeof ev.data === "string" ? ev.data : String(ev.data),
          ) as WSMessage;
          for (const fn of this.handlers) {
            fn(msg);
          }
        } catch {
          // ignore malformed messages
        }
      };
    });
  }

  subscribe(topic: string): void {
    this.send({ type: "subscribe", topic, payload: null });
  }

  unsubscribe(topic: string): void {
    this.send({ type: "unsubscribe", topic, payload: null });
  }

  onMessage(fn: MessageHandler): void {
    this.handlers.push(fn);
  }

  close(): void {
    this.ws?.close();
    this.ws = undefined;
    this.handlers = [];
  }

  private send(msg: WSMessage): void {
    if (!this.ws) throw new Error("ws: not connected");
    this.ws.send(JSON.stringify(msg));
  }
}

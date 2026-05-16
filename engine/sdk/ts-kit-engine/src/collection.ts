export interface DocumentRecord<T = unknown> {
  type: string;
  id: string;
  data: T;
  created_at: string;
  updated_at: string;
}

export interface Version {
  version: number;
  data: unknown;
  timestamp: string;
  operation: "create" | "update" | string;
}

export interface BranchHead {
  version_id: string;
  seq: number;
  parent_ids: string[];
  timestamp: string;
  live?: false;
}

export interface PrunePolicy { max_versions?: number; max_age_seconds?: number }
export interface PruneResult { versions_removed: string[]; blobs_freed: number; bytes_freed: number }

export interface CollectionQuery { limit?: number; offset?: number; sort?: string; search?: string }

export class Collection<T = unknown> {
  constructor(private baseURL: string, private type: string, private token?: string) {}

  private url(path = ""): string {
    return `${this.baseURL}/${this.type}${path}`;
  }

  private headers(method: string, body?: unknown): Record<string, string> {
    const headers: Record<string, string> = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.token && method !== "GET" && method !== "HEAD") {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    return headers;
  }

  private async request<R>(method: string, path: string, body?: unknown): Promise<R> {
    const res = await fetch(this.url(path), {
      method,
      headers: this.headers(method, body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) throw new Error(`${method} ${this.type}${path}: ${res.status}`);
    if (res.status === 204) return undefined as R;
    return (await res.json()) as R;
  }

  async create(data: T): Promise<DocumentRecord<T>> {
    return this.request<DocumentRecord<T>>("POST", "/", data);
  }

  async get(id: string): Promise<DocumentRecord<T>> {
    return this.request<DocumentRecord<T>>("GET", `/${encodeURIComponent(id)}`);
  }

  async list(q?: CollectionQuery): Promise<DocumentRecord<T>[]> {
    const p = new URLSearchParams();
    if (q?.limit !== undefined) p.set("limit", String(q.limit));
    if (q?.offset !== undefined) p.set("offset", String(q.offset));
    if (q?.sort) p.set("sort", q.sort);
    if (q?.search) p.set("search", q.search);
    const qs = p.toString();
    return this.request<DocumentRecord<T>[]>("GET", qs ? `/?${qs}` : "/");
  }

  async update(id: string, data: T): Promise<DocumentRecord<T>> {
    return this.request<DocumentRecord<T>>("PUT", `/${encodeURIComponent(id)}`, data);
  }

  async delete(id: string): Promise<void> {
    return this.request<void>("DELETE", `/${encodeURIComponent(id)}`);
  }

  async history(id: string): Promise<Version[]> {
    const payload = await this.request<{ versions: Version[] }>("GET", `/${encodeURIComponent(id)}/history`);
    return payload.versions;
  }

  async historyTopology(id: string): Promise<{ heads: string[]; versions: BranchHead[] }> {
    return this.request<{ heads: string[]; versions: BranchHead[] }>(
      "GET",
      `/${encodeURIComponent(id)}/history?topology=1`,
    );
  }

  async revert(id: string, version: number): Promise<DocumentRecord<T>> {
    return this.request<DocumentRecord<T>>("POST", `/${encodeURIComponent(id)}/revert`, { version });
  }

  async branches(id: string, opts?: { live?: boolean }): Promise<BranchHead[]> {
    const qs = opts?.live ? "?live=1" : "";
    const payload = await this.request<{ heads: BranchHead[] }>(
      "GET",
      `/${encodeURIComponent(id)}/branches${qs}`,
    );
    return payload.heads;
  }

  async fork(id: string, fromSeq: number): Promise<BranchHead> {
    return this.request<BranchHead>("POST", `/${encodeURIComponent(id)}/fork`, { from_seq: fromSeq });
  }

  async merge(id: string, sourceSeq: number, targetSeq: number, data: T): Promise<BranchHead> {
    return this.request<BranchHead>("POST", `/${encodeURIComponent(id)}/merge`, {
      source_seq: sourceSeq,
      target_seq: targetSeq,
      data,
    });
  }

  async prune(id: string, policy: PrunePolicy): Promise<PruneResult> {
    return this.request<PruneResult>("POST", `/${encodeURIComponent(id)}/prune`, policy);
  }

  async abandon(id: string, seq: number): Promise<void> {
    return this.request<void>("POST", `/${encodeURIComponent(id)}/abandon`, { seq });
  }
}

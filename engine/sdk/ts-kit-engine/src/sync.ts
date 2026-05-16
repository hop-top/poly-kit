export interface RemoteStatus {
  name: string;
  url: string;
  mode: "push" | "pull" | "both";
  filter?: string;
  connected?: boolean;
  last_sync?: string | null;
  pending_diffs?: number;
  last_error?: string | null;
  lag_ms?: number;
}

export interface RemoteConfig {
  name: string;
  url: string;
  mode?: "push" | "pull" | "both";
  filter?: string;
}

export class SyncClient {
  constructor(private baseURL: string, private token?: string) {}

  private headers(): Record<string, string> {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    return headers;
  }

  async addRemote(name: string, url: string, mode: "push" | "pull" | "both" = "both", filter = ""): Promise<RemoteConfig> {
    const res = await fetch(`${this.baseURL}/sync/remotes`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ name, url, mode, filter }),
    });
    if (!res.ok) throw new Error(`addRemote: ${res.status}`);
    return (await res.json()) as RemoteConfig;
  }

  async removeRemote(name: string): Promise<void> {
    const res = await fetch(`${this.baseURL}/sync/remotes/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: this.token ? { Authorization: `Bearer ${this.token}` } : undefined,
    });
    if (!res.ok) throw new Error(`removeRemote: ${res.status}`);
  }

  async status(): Promise<RemoteStatus[]> {
    const res = await fetch(`${this.baseURL}/sync/status`);
    if (!res.ok) throw new Error(`sync status: ${res.status}`);
    const payload = (await res.json()) as { remotes: RemoteStatus[] };
    return payload.remotes;
  }
}

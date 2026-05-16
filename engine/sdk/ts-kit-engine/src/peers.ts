export interface PeerInfo { id: string; name?: string; address: string; trusted: boolean }

export class PeerClient {
  constructor(private baseURL: string, private token?: string) {}

  async list(): Promise<PeerInfo[]> {
    const res = await fetch(`${this.baseURL}/peers`);
    if (!res.ok) throw new Error(`peers list: ${res.status}`);
    return (await res.json()) as PeerInfo[];
  }

  async trust(id: string): Promise<void> {
    const res = await fetch(`${this.baseURL}/peers/${encodeURIComponent(id)}/trust`, {
      method: "POST",
      headers: this.token ? { Authorization: `Bearer ${this.token}` } : undefined,
    });
    if (!res.ok) throw new Error(`peers trust: ${res.status}`);
  }

  async block(id: string): Promise<void> {
    const res = await fetch(`${this.baseURL}/peers/${encodeURIComponent(id)}/block`, {
      method: "POST",
      headers: this.token ? { Authorization: `Bearer ${this.token}` } : undefined,
    });
    if (!res.ok) throw new Error(`peers block: ${res.status}`);
  }
}

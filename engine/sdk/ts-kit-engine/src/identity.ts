export interface PublicKeyInfo {
  id?: string;
  public_key: string;
  publicKey: string;
  fingerprint: string;
}

export interface VerifyResult { valid: boolean }

export class IdentityClient {
  constructor(private baseURL: string, private token?: string) {}

  async publicKey(): Promise<PublicKeyInfo> {
    const res = await fetch(`${this.baseURL}/identity`);
    if (!res.ok) throw new Error(`identity publicKey: ${res.status}`);
    const payload = (await res.json()) as Omit<PublicKeyInfo, "publicKey">;
    return { ...payload, publicKey: payload.public_key };
  }

  async verify(data: string, signature: string): Promise<VerifyResult> {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    const res = await fetch(`${this.baseURL}/identity/verify`, {
      method: "POST",
      headers,
      body: JSON.stringify({ data, signature }),
    });
    if (!res.ok) throw new Error(`identity verify: ${res.status}`);
    return (await res.json()) as VerifyResult;
  }
}

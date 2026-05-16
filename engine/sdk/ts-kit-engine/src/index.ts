import { spawn, type ChildProcess } from "child_process";
import { Collection } from "./collection";
import { EventStream } from "./events";
import { SyncClient } from "./sync";
import { PeerClient } from "./peers";
import { IdentityClient } from "./identity";
export {
  Collection,
  type BranchHead,
  type CollectionQuery,
  type DocumentRecord,
  type PrunePolicy,
  type PruneResult,
  type Version,
} from "./collection";
export { EventStream } from "./events";
export { SyncClient, type RemoteStatus } from "./sync";
export { PeerClient, type PeerInfo } from "./peers";
export { IdentityClient, type PublicKeyInfo } from "./identity";

export interface KitEngineOptions {
  app?: string;
  data?: string;
  port?: number;
  daemon?: boolean;
  encrypt?: boolean;
  noPeer?: boolean;
  noSync?: boolean;
  binPath?: string;
}

export class KitEngine {
  readonly port: number;
  readonly pid: number;
  readonly token?: string;
  readonly shutdownToken?: string;
  private process?: ChildProcess;
  private _events?: EventStream;

  private constructor(
    port: number,
    pid: number,
    token?: string,
    shutdownToken?: string,
    proc?: ChildProcess,
  ) {
    this.port = port;
    this.pid = pid;
    this.token = token;
    this.shutdownToken = shutdownToken;
    this.process = proc;
  }

  static async start(opts?: KitEngineOptions): Promise<KitEngine> {
    const bin = opts?.binPath ?? "kit";
    const args = ["serve", `--port`, String(opts?.port ?? 0)];
    if (opts?.app) args.push("--app", opts.app);
    if (opts?.data) args.push("--data", opts.data);
    if (opts?.daemon) args.push("--daemon");
    if (opts?.encrypt) args.push("--encrypt");
    if (opts?.noPeer) args.push("--no-peer");
    if (opts?.noSync) args.push("--no-sync");

    const proc = spawn(bin, args, { stdio: ["ignore", "pipe", "ignore"] });

    const info = await new Promise<{ port: number; pid: number; token: string; shutdown_token: string }>((resolve, reject) => {
      let buf = "";
      proc.stdout!.on("data", (chunk: Buffer) => {
        buf += chunk.toString();
        const nl = buf.indexOf("\n");
        if (nl !== -1) {
          try {
            resolve(JSON.parse(buf.slice(0, nl)));
          } catch (e) {
            reject(new Error(`kit serve: invalid startup JSON: ${buf.slice(0, nl)}`));
          }
        }
      });
      proc.on("error", reject);
      proc.on("close", (code) => reject(new Error(`kit serve exited with ${code}`)));
    });

    const res = await fetch(`http://127.0.0.1:${info.port}/health`);
    if (!res.ok) throw new Error(`kit serve health check failed: ${res.status}`);

    return new KitEngine(info.port, info.pid, info.token, info.shutdown_token, proc);
  }

  static async connect(port: number, token?: string, shutdownToken?: string): Promise<KitEngine> {
    const res = await fetch(`http://127.0.0.1:${port}/health`);
    if (!res.ok) throw new Error(`connect health check failed: ${res.status}`);
    const data = (await res.json()) as { pid: number };
    return new KitEngine(port, data.pid, token, shutdownToken);
  }

  private get baseURL(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  collection<T = unknown>(type: string): Collection<T> {
    return new Collection<T>(this.baseURL, type, this.token);
  }

  get events(): EventStream {
    if (!this._events) {
      this._events = new EventStream(`ws://127.0.0.1:${this.port}/events`);
      this._events.connect();
    }
    return this._events;
  }

  private _sync?: SyncClient;
  private _peers?: PeerClient;
  private _identity?: IdentityClient;

  get sync(): SyncClient {
    if (!this._sync) this._sync = new SyncClient(this.baseURL, this.token);
    return this._sync;
  }

  get peers(): PeerClient {
    if (!this._peers) this._peers = new PeerClient(this.baseURL, this.token);
    return this._peers;
  }

  get identity(): IdentityClient {
    if (!this._identity) this._identity = new IdentityClient(this.baseURL, this.token);
    return this._identity;
  }

  async stop(): Promise<void> {
    const headers: Record<string, string> = {};
    const tok = this.shutdownToken ?? this.token;
    if (tok) headers["Authorization"] = `Bearer ${tok}`;
    await fetch(`${this.baseURL}/shutdown`, { method: "POST", headers });
    if (this.process) {
      await new Promise<void>((resolve) => {
        this.process!.on("close", () => resolve());
      });
      this.process = undefined;
    }
  }
}

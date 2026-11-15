import { createHash } from "crypto";
import { createServer, type IncomingMessage, type Server } from "http";
import type { Socket } from "net";
import type { AddressInfo } from "net";

/** One accepted client of the scripted server. */
export interface Peer {
  headers: IncomingMessage["headers"];
  received: Array<Record<string, unknown>>;
  send(frame: unknown): void;
  drop(): void;
}

function textFrame(s: string): Buffer {
  const p = Buffer.from(s);
  const head =
    p.length < 126
      ? Buffer.from([0x81, p.length])
      : Buffer.from([0x81, 126, p.length >> 8, p.length & 0xff]);
  return Buffer.concat([head, p]);
}

// Decode masked client text frames (enough for small control JSON).
function decode(buf: Buffer, out: string[]): Buffer {
  while (buf.length >= 2) {
    let len = buf[1] & 0x7f;
    let off = 2;
    if (len === 126) {
      if (buf.length < 4) break;
      len = buf.readUInt16BE(2);
      off = 4;
    }
    if (buf.length < off + 4 + len) break;
    const mask = buf.subarray(off, off + 4);
    const data = Buffer.from(buf.subarray(off + 4, off + 4 + len));
    for (let i = 0; i < data.length; i++) data[i] ^= mask[i % 4];
    if ((buf[0] & 0x0f) === 1) out.push(data.toString());
    buf = buf.subarray(off + 4 + len);
  }
  return buf;
}

/** Minimal loopback WebSocket server; calls onPeer per connection. */
export async function wsServer(onPeer: (p: Peer) => void) {
  const sockets = new Set<Socket>();
  const server: Server = createServer();
  server.on("upgrade", (req, sock: Socket) => {
    sockets.add(sock);
    sock.on("close", () => sockets.delete(sock));
    sock.on("error", () => {});
    const accept = createHash("sha1")
      .update(req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
      .digest("base64");
    sock.write(
      "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n" +
        `Connection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    const peer: Peer = {
      headers: req.headers,
      received: [],
      send: (f) => sock.write(textFrame(JSON.stringify(f))),
      drop: () => sock.destroy(),
    };
    let buf: Buffer = Buffer.alloc(0);
    sock.on("data", (d) => {
      const msgs: string[] = [];
      buf = decode(Buffer.concat([buf, d]), msgs);
      for (const m of msgs) peer.received.push(JSON.parse(m));
    });
    onPeer(peer);
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  return {
    url: `ws://127.0.0.1:${(server.address() as AddressInfo).port}/events`,
    close: async () => {
      for (const s of sockets) s.destroy();
      await new Promise<void>((r) => server.close(() => r()));
    },
  };
}

export async function until(cond: () => boolean, ms = 4000): Promise<void> {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) throw new Error("until: timeout");
    await new Promise((r) => setTimeout(r, 10));
  }
}

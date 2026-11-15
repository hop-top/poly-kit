import { afterEach, describe, expect, it } from "vitest";
import { EventStream } from "../src/events";
import { type Peer, until, wsServer } from "./ws-server";

const msg = (topic: string, payload: unknown) => ({ type: "message", topic, payload });

describe("EventStream", () => {
  const cleanup: Array<() => Promise<void> | void> = [];
  afterEach(async () => {
    for (const c of cleanup.splice(0)) await c();
  });

  async function setup(token?: string) {
    const peers: Peer[] = [];
    const srv = await wsServer((p) => peers.push(p));
    const es = new EventStream(srv.url, token);
    cleanup.push(() => es.close(), srv.close);
    return { peers, es };
  }

  it("sends one subscribe frame per topic and decodes message payloads", async () => {
    const { peers, es } = await setup();
    const got: unknown[] = [];
    es.on("kit.engine.document.*", (d) => got.push(d));
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 1);
    expect(peers[0].received[0]).toEqual({ type: "subscribe", topic: "kit.engine.document.*" });
    peers[0].send(msg("kit.engine.document.created", { id: "a" }));
    await until(() => got.length === 1);
    expect(got[0]).toEqual({ id: "a" });
  });

  it("subscribes immediately when on() is called after connect", async () => {
    const { peers, es } = await setup();
    es.connect();
    await until(() => peers.length === 1);
    await new Promise((r) => setTimeout(r, 50));
    es.on("a.b", () => {});
    await until(() => peers[0].received.length === 1);
    expect(peers[0].received[0]).toEqual({ type: "subscribe", topic: "a.b" });
  });

  it("skips control and malformed frames", async () => {
    const { peers, es } = await setup();
    const got: unknown[] = [];
    es.on("a.b", (d) => got.push(d));
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 1);
    peers[0].send({ type: "welcome" });
    peers[0].send({ type: "ack", topic: "a.b" });
    peers[0].send({ type: "error", topic: "a.b" });
    peers[0].send({ type: "a.b", data: { old: true } });
    peers[0].send({ type: "message", payload: 1 });
    peers[0].send("not an object");
    peers[0].send(msg("a.b", 7));
    await until(() => got.length === 1);
    await new Promise((r) => setTimeout(r, 50));
    expect(got).toEqual([7]);
  });

  it("matches * as one segment and ** as one or more", async () => {
    const { peers, es } = await setup();
    const one: string[] = [];
    const many: string[] = [];
    es.on("a.*", (d) => one.push(d as string));
    es.on("a.**", (d) => many.push(d as string));
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 2);
    for (const t of ["a.b", "a.b.c", "a", "x.b"]) peers[0].send(msg(t, t));
    await until(() => many.length === 2);
    await new Promise((r) => setTimeout(r, 50));
    expect(one).toEqual(["a.b"]);
    expect(many).toEqual(["a.b", "a.b.c"]);
  });

  it("sends bare * as ** and delivers every topic", async () => {
    const { peers, es } = await setup();
    const got: string[] = [];
    es.on("*", (d) => got.push(d as string));
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 1);
    expect(peers[0].received[0]).toEqual({ type: "subscribe", topic: "**" });
    peers[0].send(msg("a", "a"));
    peers[0].send(msg("a.b.c", "a.b.c"));
    await until(() => got.length === 2);
  });

  it("off() stops delivery and unsubscribes", async () => {
    const { peers, es } = await setup();
    const got: unknown[] = [];
    const h = (d: unknown) => got.push(d);
    es.on("a.b", h);
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 1);
    es.off("a.b", h);
    await until(() => peers[0].received.length === 2);
    expect(peers[0].received[1]).toEqual({ type: "unsubscribe", topic: "a.b" });
    peers[0].send(msg("a.b", 1));
    await new Promise((r) => setTimeout(r, 50));
    expect(got).toEqual([]);
  });

  it("sends the bearer token when set", async () => {
    const { peers, es } = await setup("tok");
    es.connect();
    await until(() => peers.length === 1);
    expect(peers[0].headers.authorization).toBe("Bearer tok");
  });

  it("reconnects and resubscribes after a drop", async () => {
    const { peers, es } = await setup();
    const got: unknown[] = [];
    es.on("a.b", (d) => got.push(d));
    es.connect();
    await until(() => peers.length === 1 && peers[0].received.length === 1);
    peers[0].drop();
    await until(() => peers.length === 2 && peers[1].received.length === 1, 5000);
    expect(peers[1].received[0]).toEqual({ type: "subscribe", topic: "a.b" });
    peers[1].send(msg("a.b", 1));
    await until(() => got.length === 1);
  });

  it("does not reconnect after close()", async () => {
    const { peers, es } = await setup();
    es.connect();
    await until(() => peers.length === 1);
    es.close();
    await new Promise((r) => setTimeout(r, 1500));
    expect(peers.length).toBe(1);
  });
});

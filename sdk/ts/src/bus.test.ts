import { describe, it, expect } from "vitest";
import {
  createBus,
  createEvent,
  MemoryAdapter,
  type Adapter,
  type Event,
  type Bus,
} from "./bus";

describe("createEvent", () => {
  it("sets topic, source, payload and timestamp", () => {
    const before = new Date();
    const e = createEvent("llm.request", "src", { model: "claude" });
    const after = new Date();

    expect(e.topic).toBe("llm.request");
    expect(e.source).toBe("src");
    expect(e.payload).toEqual({ model: "claude" });
    expect(e.timestamp.getTime()).toBeGreaterThanOrEqual(before.getTime());
    expect(e.timestamp.getTime()).toBeLessThanOrEqual(after.getTime());
  });
});

describe("Bus", () => {
  let bus: Bus;

  it("delivers event to exact-match subscriber", () => {
    bus = createBus();
    const got: Event[] = [];
    bus.subscribe("test.event", (e) => { got.push(e); });

    const e = createEvent("test.event", "src", "hello");
    bus.publish(e);

    expect(got).toHaveLength(1);
    expect(got[0].source).toBe("src");
  });

  it("does not deliver to non-matching subscriber", () => {
    bus = createBus();
    let called = false;
    bus.subscribe("llm.request", () => { called = true; });

    bus.publish(createEvent("llm.response", "src", null));
    expect(called).toBe(false);
  });

  it("supports * wildcard matching one segment", () => {
    bus = createBus();
    const got: string[] = [];
    bus.subscribe("llm.*", (e) => { got.push(e.topic); });

    bus.publish(createEvent("llm.request", "src", null));
    bus.publish(createEvent("llm.response", "src", null));
    bus.publish(createEvent("llm.request.start", "src", null)); // too deep
    bus.publish(createEvent("tool.exec", "src", null)); // wrong prefix

    expect(got).toEqual(["llm.request", "llm.response"]);
  });

  it("supports * in any position", () => {
    bus = createBus();
    const got: string[] = [];
    bus.subscribe("*.request", (e) => { got.push(e.topic); });

    bus.publish(createEvent("llm.request", "src", null));
    bus.publish(createEvent("tool.request", "src", null));
    bus.publish(createEvent("llm.response", "src", null));

    expect(got).toEqual(["llm.request", "tool.request"]);
  });

  it("supports # wildcard matching zero or more trailing segments", () => {
    bus = createBus();
    const got: string[] = [];
    bus.subscribe("llm.#", (e) => { got.push(e.topic); });

    bus.publish(createEvent("llm.request", "src", null));
    bus.publish(createEvent("llm.request.start", "src", null));
    bus.publish(createEvent("llm", "src", null));
    bus.publish(createEvent("tool.exec", "src", null));

    expect(got).toEqual(["llm.request", "llm.request.start", "llm"]);
  });

  it("# alone matches everything", () => {
    bus = createBus();
    const got: string[] = [];
    bus.subscribe("#", (e) => { got.push(e.topic); });

    bus.publish(createEvent("llm.request", "src", null));
    bus.publish(createEvent("anything", "src", null));

    expect(got).toEqual(["llm.request", "anything"]);
  });

  it("unsubscribe stops delivery", () => {
    bus = createBus();
    let count = 0;
    const unsub = bus.subscribe("test.event", () => { count++; });

    bus.publish(createEvent("test.event", "src", null));
    expect(count).toBe(1);

    unsub();
    bus.publish(createEvent("test.event", "src", null));
    expect(count).toBe(1);
  });

  it("multiple subscribers all receive events", () => {
    bus = createBus();
    let a = 0, b = 0;
    bus.subscribe("test.event", () => { a++; });
    bus.subscribe("test.event", () => { b++; });

    bus.publish(createEvent("test.event", "src", null));
    expect(a).toBe(1);
    expect(b).toBe(1);
  });

  it("async handlers run via microtask", async () => {
    bus = createBus();
    let called = false;
    bus.subscribe("test.event", async () => {
      called = true;
    });

    bus.publish(createEvent("test.event", "src", null));
    // async handler scheduled but not yet run
    await Promise.resolve();
    // after microtask flush
    expect(called).toBe(true);
  });

  it("close prevents further publish", () => {
    bus = createBus();
    bus.close();
    expect(() => {
      bus.publish(createEvent("test", "src", null));
    }).toThrow(/closed/);
  });
});

describe("MemoryAdapter", () => {
  it("implements Adapter interface directly", () => {
    const adapter: Adapter = new MemoryAdapter();
    const got: Event[] = [];
    adapter.subscribe("test.event", (e) => { got.push(e); });

    adapter.publish(createEvent("test.event", "src", "hello"));
    expect(got).toHaveLength(1);
    expect(got[0].source).toBe("src");
    adapter.close();
  });
});

describe("createBus with adapter", () => {
  it("accepts a custom adapter", () => {
    const adapter = new MemoryAdapter();
    const bus = createBus(adapter);
    const got: Event[] = [];
    bus.subscribe("test.event", (e) => { got.push(e); });

    bus.publish(createEvent("test.event", "src", "hello"));
    expect(got).toHaveLength(1);
    bus.close();
  });

  it("defaults to MemoryAdapter when no adapter given", () => {
    const bus = createBus();
    const got: Event[] = [];
    bus.subscribe("x", (e) => { got.push(e); });
    bus.publish(createEvent("x", "src", null));
    expect(got).toHaveLength(1);
    bus.close();
  });
});

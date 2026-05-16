/**
 * In-memory pub/sub with MQTT-style topic matching.
 *
 * Wildcards (dot-separated segments):
 *   `*` — matches exactly one segment
 *   `#` — matches zero or more trailing segments
 */

export interface Event {
  topic: string;
  source: string;
  timestamp: Date;
  payload: any;
}

export type Handler = (event: Event) => void | Promise<void>;
export type Unsubscribe = () => void;

export interface Bus {
  publish(event: Event): void;
  subscribe(pattern: string, handler: Handler): Unsubscribe;
  close(): void;
}

/** Adapter is the pluggable transport layer for the bus. */
export interface Adapter {
  publish(event: Event): void;
  subscribe(pattern: string, handler: Handler): Unsubscribe;
  close(): void;
}

export function createEvent(
  topic: string,
  source: string,
  payload: any,
): Event {
  return { topic, source, timestamp: new Date(), payload };
}

// MQTT-style pattern match against a dot-separated topic.
export function matchTopic(topic: string, pattern: string): boolean {
  const tParts = topic.split(".");
  const pParts = pattern.split(".");

  let ti = 0;
  let pi = 0;

  while (pi < pParts.length) {
    if (pParts[pi] === "#") {
      // # must be last segment per MQTT convention
      return pi === pParts.length - 1;
    }
    if (ti >= tParts.length) return false;
    if (pParts[pi] !== "*" && pParts[pi] !== tParts[ti]) return false;
    ti++;
    pi++;
  }

  return ti === tParts.length;
}

interface Subscription {
  id: number;
  pattern: string;
  handler: Handler;
}

/** MemoryAdapter is the default in-process adapter. */
export class MemoryAdapter implements Adapter {
  private subs: Subscription[] = [];
  private nextId = 0;
  private closed = false;

  publish(event: Event): void {
    if (this.closed) throw new Error("bus: publish after closed");

    const matching = this.subs.filter((s) =>
      matchTopic(event.topic, s.pattern),
    );

    for (const s of matching) {
      const result = s.handler(event);
      if (result && typeof (result as Promise<void>).then === "function") {
        Promise.resolve(result).then(
          () => {},
          () => {},
        );
      }
    }
  }

  subscribe(pattern: string, handler: Handler): Unsubscribe {
    const id = this.nextId++;
    this.subs.push({ id, pattern, handler });
    return () => {
      this.subs = this.subs.filter((s) => s.id !== id);
    };
  }

  close(): void {
    this.closed = true;
    this.subs = [];
  }
}

/** Creates a Bus backed by the given adapter (default: MemoryAdapter). */
export function createBus(adapter?: Adapter): Bus {
  const a = adapter ?? new MemoryAdapter();
  return {
    publish: (event) => a.publish(event),
    subscribe: (pattern, handler) => a.subscribe(pattern, handler),
    close: () => a.close(),
  };
}

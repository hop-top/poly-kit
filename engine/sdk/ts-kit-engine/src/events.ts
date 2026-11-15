type Handler = (data: unknown) => void;

/**
 * Hub topic match: `*` one segment, `**` one or more. The bare `*`
 * means every topic.
 */
function matches(pattern: string, topic: string): boolean {
  if (pattern === "*") return true;
  const seg = (p: string[], t: string[]): boolean => {
    if (p.length === 0) return t.length === 0;
    if (t.length === 0) return false;
    if (p[0] === "**") {
      for (let i = 1; i <= t.length; i++) {
        if (seg(p.slice(1), t.slice(i))) return true;
      }
      return false;
    }
    return (p[0] === "*" || p[0] === t[0]) && seg(p.slice(1), t.slice(1));
  };
  return seg(pattern.split("."), topic.split("."));
}

// The hub's `*` is one segment, so "everything" goes out as `**`.
const wire = (pattern: string): string => (pattern === "*" ? "**" : pattern);

/**
 * Subscription to the engine `/events` socket. `on(pattern, handler)`
 * subscribes to a hub topic pattern and calls `handler` with the
 * `payload` of each matching `message` frame. `welcome`, `ack`, `error`
 * and malformed frames are skipped. A dropped socket reconnects and
 * resubscribes after 1s.
 */
export class EventStream {
  private ws?: WebSocket;
  private handlers = new Map<string, Set<Handler>>();
  private wsURL: string;
  private token?: string;
  private closed = false;
  private reconnectTimer?: ReturnType<typeof setTimeout>;

  constructor(wsURL: string, token?: string) {
    this.wsURL = wsURL;
    this.token = token;
  }

  connect(): void {
    if (this.closed) return;
    // Node's WebSocket accepts a headers init as the second argument;
    // the DOM typings do not know it.
    const init = this.token
      ? ({ headers: { Authorization: `Bearer ${this.token}` } } as unknown as string[])
      : undefined;
    const ws = new WebSocket(this.wsURL, init);

    ws.onopen = () => {
      for (const p of new Set([...this.handlers.keys()].map(wire))) {
        this.send({ type: "subscribe", topic: p });
      }
    };

    ws.onmessage = (ev: MessageEvent) => {
      let frame: { type?: unknown; topic?: unknown; payload?: unknown };
      try {
        frame = JSON.parse(typeof ev.data === "string" ? ev.data : String(ev.data));
      } catch {
        return; // malformed
      }
      if (!frame || frame.type !== "message" || typeof frame.topic !== "string") return;
      const topic = frame.topic;
      for (const [pattern, fns] of this.handlers) {
        if (!matches(pattern, topic)) continue;
        for (const fn of [...fns]) {
          try {
            fn(frame.payload);
          } catch { /* handler errors stay out of the stream */ }
        }
      }
    };

    ws.onclose = () => {
      if (this.ws === ws) this.ws = undefined;
      if (!this.closed) this.reconnectTimer = setTimeout(() => this.connect(), 1000);
    };

    this.ws = ws;
  }

  on(pattern: string, handler: Handler): void {
    let set = this.handlers.get(pattern);
    if (!set) {
      set = new Set();
      this.handlers.set(pattern, set);
      if (!this.wireHeld(pattern)) this.send({ type: "subscribe", topic: wire(pattern) });
    }
    set.add(handler);
  }

  off(pattern: string, handler: Handler): void {
    const set = this.handlers.get(pattern);
    if (!set) return;
    set.delete(handler);
    if (set.size === 0) {
      this.handlers.delete(pattern);
      if (!this.wireHeld(pattern)) this.send({ type: "unsubscribe", topic: wire(pattern) });
    }
  }

  close(): void {
    this.closed = true;
    clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = undefined;
    this.handlers.clear();
  }

  // Another registered pattern maps to the same wire topic.
  private wireHeld(pattern: string): boolean {
    const w = wire(pattern);
    for (const k of this.handlers.keys()) if (k !== pattern && wire(k) === w) return true;
    return false;
  }

  private send(frame: object): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(frame));
  }
}

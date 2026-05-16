type Handler = (data: unknown) => void;

export class EventStream {
  private ws?: WebSocket;
  private handlers = new Map<string, Set<Handler>>();
  private wsURL: string;
  private reconnectTimer?: ReturnType<typeof setTimeout>;

  constructor(wsURL: string) {
    this.wsURL = wsURL;
  }

  connect(): void {
    const ws = new WebSocket(this.wsURL);

    ws.onmessage = (ev: MessageEvent) => {
      try {
        const msg = JSON.parse(typeof ev.data === "string" ? ev.data : String(ev.data));
        const fns = this.handlers.get(msg.type);
        if (fns) for (const fn of fns) fn(msg.data);
      } catch { /* ignore malformed */ }
    };

    ws.onclose = () => {
      this.reconnectTimer = setTimeout(() => this.connect(), 1000);
    };

    this.ws = ws;
  }

  on(event: string, handler: Handler): void {
    let set = this.handlers.get(event);
    if (!set) {
      set = new Set();
      this.handlers.set(event, set);
    }
    set.add(handler);
  }

  off(event: string, handler: Handler): void {
    this.handlers.get(event)?.delete(handler);
  }

  close(): void {
    clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = undefined;
    this.handlers.clear();
  }
}

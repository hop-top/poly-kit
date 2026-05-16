import { describe, it, expect } from "vitest";
import { RPCClient } from "./rpc";
import type { Entity } from "./api";

interface Item extends Entity {
  id: string;
  name: string;
}

describe("RPCClient", () => {
  it("constructor stores options", () => {
    const client = new RPCClient<Item>({
      baseURL: "http://localhost:8080",
      auth: "tok_123",
    });
    expect(client).toBeDefined();
  });

  it("constructor works without auth", () => {
    const client = new RPCClient<Item>({
      baseURL: "http://localhost:8080",
    });
    expect(client).toBeDefined();
  });

  it("exposes CRUD methods", () => {
    const client = new RPCClient<Item>({
      baseURL: "http://localhost:8080",
    });
    expect(typeof client.create).toBe("function");
    expect(typeof client.get).toBe("function");
    expect(typeof client.list).toBe("function");
    expect(typeof client.update).toBe("function");
    expect(typeof client.delete).toBe("function");
  });

  it("methods return promises", () => {
    const client = new RPCClient<Item>({
      baseURL: "http://localhost:8080",
    });
    // Verify async signatures — calls will fail without a server
    // but should return promises (not throw synchronously)
    const p = client.get("1");
    expect(p).toBeInstanceOf(Promise);
    p.catch(() => {}); // suppress unhandled rejection
  });
});

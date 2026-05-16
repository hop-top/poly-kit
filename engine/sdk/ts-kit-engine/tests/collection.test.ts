import { describe, it, expect, vi, beforeEach } from "vitest";
import { Collection } from "../src/collection";

interface Item {
  name: string;
}

function mockFetch(status: number, body?: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  });
}

describe("Collection", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("create sends POST to /{type}/", async () => {
    const created = { type: "notes", id: "1", data: { name: "foo" }, created_at: "t", updated_at: "t" };
    const f = mockFetch(201, created);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes", "tok");
    const result = await col.create({ name: "foo" });

    expect(result).toEqual(created);
    expect(f).toHaveBeenCalledWith(
      "http://localhost:9000/notes/",
      expect.objectContaining({ method: "POST", headers: expect.objectContaining({ Authorization: "Bearer tok" }) }),
    );
  });

  it("get sends GET to /{type}/{id}", async () => {
    const item = { type: "notes", id: "42", data: { name: "bar" }, created_at: "t", updated_at: "t" };
    const f = mockFetch(200, item);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    const result = await col.get("42");

    expect(result).toEqual(item);
    expect(f).toHaveBeenCalledWith(
      "http://localhost:9000/notes/42",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("list appends query params", async () => {
    const items = [{ type: "notes", id: "1", data: { name: "a" }, created_at: "t", updated_at: "t" }];
    const f = mockFetch(200, items);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    await col.list({ limit: 10, sort: "name" });

    expect(f).toHaveBeenCalledWith(
      expect.stringContaining("limit=10"),
      expect.anything(),
    );
  });

  it("update sends PUT to /{type}/{id}", async () => {
    const item = { type: "notes", id: "1", data: { name: "updated" }, created_at: "t", updated_at: "t" };
    const f = mockFetch(200, item);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    await col.update("1", { name: "updated" });

    expect(f).toHaveBeenCalledWith(
      "http://localhost:9000/notes/1",
      expect.objectContaining({ method: "PUT" }),
    );
  });

  it("delete sends DELETE to /{type}/{id}", async () => {
    const f = mockFetch(204);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    await col.delete("1");

    expect(f).toHaveBeenCalledWith(
      "http://localhost:9000/notes/1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("history sends GET to /{type}/{id}/history and unwraps versions", async () => {
    const versions = [{ version: 1, timestamp: "t", data: {}, operation: "create" }];
    const f = mockFetch(200, { versions });
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    const result = await col.history("1");

    expect(result).toEqual(versions);
    expect(f).toHaveBeenCalledWith(
      "http://localhost:9000/notes/1/history",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("throws on non-ok response", async () => {
    const f = mockFetch(404);
    vi.stubGlobal("fetch", f);

    const col = new Collection<Item>("http://localhost:9000", "notes");
    await expect(col.get("missing")).rejects.toThrow("404");
  });
});

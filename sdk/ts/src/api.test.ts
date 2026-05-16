import { describe, it, expect, vi } from "vitest";
import {
  APIClient,
  APIRequestError,
  type Entity,
  type APIError,
} from "./api";

interface Item extends Entity {
  id: string;
  name: string;
}

function mockFetch(status: number, body?: unknown): typeof fetch {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: "mock",
    json: () => Promise.resolve(body),
  }) as unknown as typeof fetch;
}

describe("APIClient", () => {
  const base = "http://localhost:8080/items";

  it("create sends POST and returns entity", async () => {
    const created: Item = { id: "1", name: "foo" };
    const f = mockFetch(201, created);
    const client = new APIClient<Item>(base, { fetch: f });

    const result = await client.create({ name: "foo" });

    expect(result).toEqual(created);
    expect(f).toHaveBeenCalledWith(
      `${base}/`,
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("get sends GET /{id}", async () => {
    const item: Item = { id: "42", name: "bar" };
    const f = mockFetch(200, item);
    const client = new APIClient<Item>(base, { fetch: f });

    const result = await client.get("42");

    expect(result).toEqual(item);
    expect(f).toHaveBeenCalledWith(
      `${base}/42`,
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("list sends GET with query params", async () => {
    const items: Item[] = [{ id: "1", name: "a" }];
    const f = mockFetch(200, items);
    const client = new APIClient<Item>(base, { fetch: f });

    const result = await client.list({
      limit: 10,
      offset: 5,
      sort: "name",
      search: "test",
    });

    expect(result).toEqual(items);
    const url = (f as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("limit=10");
    expect(url).toContain("offset=5");
    expect(url).toContain("sort=name");
    expect(url).toContain("search=test");
  });

  it("list sends GET without params when query omitted", async () => {
    const f = mockFetch(200, []);
    const client = new APIClient<Item>(base, { fetch: f });

    await client.list();

    expect(f).toHaveBeenCalledWith(
      `${base}/`,
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("update sends PUT /{id}", async () => {
    const item: Item = { id: "7", name: "updated" };
    const f = mockFetch(200, item);
    const client = new APIClient<Item>(base, { fetch: f });

    const result = await client.update(item);

    expect(result).toEqual(item);
    expect(f).toHaveBeenCalledWith(
      `${base}/7`,
      expect.objectContaining({ method: "PUT" }),
    );
  });

  it("delete sends DELETE /{id}", async () => {
    const f = mockFetch(204);
    const client = new APIClient<Item>(base, { fetch: f });

    await client.delete("99");

    expect(f).toHaveBeenCalledWith(
      `${base}/99`,
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("throws APIRequestError on non-2xx", async () => {
    const errBody: APIError = {
      status: 404,
      code: "not_found",
      message: "not found",
    };
    const f = mockFetch(404, errBody);
    const client = new APIClient<Item>(base, { fetch: f });

    try {
      await client.get("missing");
      expect.fail("should have thrown");
    } catch (e) {
      expect(e).toBeInstanceOf(APIRequestError);
      const err = e as APIRequestError;
      expect(err.status).toBe(404);
      expect(err.code).toBe("not_found");
      expect(err.message).toBe("not found");
    }
  });

  it("sets Authorization header when auth provided", async () => {
    const f = mockFetch(200, []);
    const client = new APIClient<Item>(base, {
      fetch: f,
      auth: "tok_123",
    });

    await client.list();

    const headers = (f as ReturnType<typeof vi.fn>).mock.calls[0][1]
      .headers as Record<string, string>;
    expect(headers["Authorization"]).toBe("Bearer tok_123");
  });

  it("omits Authorization header when no auth", async () => {
    const f = mockFetch(200, []);
    const client = new APIClient<Item>(base, { fetch: f });

    await client.list();

    const headers = (f as ReturnType<typeof vi.fn>).mock.calls[0][1]
      .headers as Record<string, string>;
    expect(headers["Authorization"]).toBeUndefined();
  });

  it("sets Content-Type header", async () => {
    const f = mockFetch(200, []);
    const client = new APIClient<Item>(base, { fetch: f });

    await client.list();

    const headers = (f as ReturnType<typeof vi.fn>).mock.calls[0][1]
      .headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
  });

  it("strips trailing slash from baseURL", async () => {
    const f = mockFetch(200, { id: "1", name: "x" });
    const client = new APIClient<Item>(base + "///", { fetch: f });

    await client.get("1");

    expect(f).toHaveBeenCalledWith(
      `${base}/1`,
      expect.anything(),
    );
  });

  it("encodes special characters in id", async () => {
    const f = mockFetch(200, { id: "a/b", name: "x" });
    const client = new APIClient<Item>(base, { fetch: f });

    await client.get("a/b");

    const url = (f as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain(encodeURIComponent("a/b"));
  });
});

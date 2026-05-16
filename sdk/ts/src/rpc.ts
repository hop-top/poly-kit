/**
 * Typed ConnectRPC client for kit EntityService.
 *
 * Thin wrapper over generated Connect stubs, mapping between
 * JS objects and protobuf Struct (JSON intermediate).
 */

import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { create, type JsonObject } from "@bufbuild/protobuf";
import {
  EntityService,
  CreateRequestSchema,
  GetRequestSchema,
  ListRequestSchema,
  UpdateRequestSchema,
  DeleteRequestSchema,
} from "./gen/crud_pb.js";
import type { Entity, Query } from "./api.js";

export interface RPCClientOptions {
  baseURL: string;
  auth?: string;
}

export class RPCClient<T extends Entity> {
  private client: Client<typeof EntityService>;
  private auth?: string;

  constructor(opts: RPCClientOptions) {
    this.auth = opts.auth;
    const transport = createConnectTransport({
      baseUrl: opts.baseURL,
      interceptors: this.auth
        ? [
            (next) => async (req) => {
              req.header.set(
                "Authorization",
                `Bearer ${this.auth}`,
              );
              return next(req);
            },
          ]
        : [],
    });
    this.client = createClient(EntityService, transport);
  }

  async create(entity: Omit<T, "id"> | T): Promise<T> {
    const res = await this.client.create(
      create(CreateRequestSchema, {
        entity: entity as unknown as JsonObject,
      }),
    );
    return (res.entity ?? {}) as unknown as T;
  }

  async get(id: string): Promise<T> {
    const res = await this.client.get(
      create(GetRequestSchema, { id }),
    );
    return (res.entity ?? {}) as unknown as T;
  }

  async list(q?: Query): Promise<T[]> {
    const res = await this.client.list(
      create(ListRequestSchema, {
        limit: q?.limit ?? 0,
        offset: q?.offset ?? 0,
        sort: q?.sort ?? "",
        search: q?.search ?? "",
      }),
    );
    return (res.entities ?? []) as unknown as T[];
  }

  async update(entity: T): Promise<T> {
    const res = await this.client.update(
      create(UpdateRequestSchema, {
        entity: entity as unknown as JsonObject,
      }),
    );
    return (res.entity ?? {}) as unknown as T;
  }

  async delete(id: string): Promise<void> {
    await this.client.delete(
      create(DeleteRequestSchema, { id }),
    );
  }
}

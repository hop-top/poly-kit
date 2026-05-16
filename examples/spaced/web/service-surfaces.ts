/**
 * service-surfaces.ts — browser-safe samples for adjacent spaced services.
 *
 * These are intentionally static/demo adapters: the web sample has no Node
 * server at runtime, so each surface describes how the existing command/data
 * model would be exposed by REST, WebSocket/event streams, and MCP.
 */

import { DAEMONS, MISSIONS, VEHICLES, findMission } from '../ts/data';

export type SurfaceKind = 'http' | 'websocket' | 'mcp' | 'events';

export interface ServiceEndpoint {
  method: string;
  path: string;
  purpose: string;
  sample: string;
}

export interface ServiceSurface {
  id: string;
  name: string;
  kind: SurfaceKind;
  transport: string;
  status: 'sample' | 'ready';
  summary: string;
  endpoints: ServiceEndpoint[];
}

const mission = findMission('Starman') ?? MISSIONS[0];

export const SERVICE_SURFACES: ServiceSurface[] = [
  {
    id: 'rest',
    name: 'Mission Archive API',
    kind: 'http',
    transport: 'HTTP JSON',
    status: 'ready',
    summary: 'Read-only JSON endpoints matching the Go serve command shape.',
    endpoints: [
      {
        method: 'GET',
        path: '/missions',
        purpose: 'List every mission in the sample archive.',
        sample: JSON.stringify(MISSIONS.slice(0, 2), null, 2),
      },
      {
        method: 'GET',
        path: '/missions/{id}',
        purpose: 'Fetch one mission by display name or slug.',
        sample: JSON.stringify(mission, null, 2),
      },
      {
        method: 'GET',
        path: '/fleet',
        purpose: 'List vehicles from the shared spaced data layer.',
        sample: JSON.stringify(VEHICLES.map(v => ({ name: v.name, status: v.status })), null, 2),
      },
    ],
  },
  {
    id: 'websocket',
    name: 'Telemetry Socket',
    kind: 'websocket',
    transport: 'WebSocket JSON frames',
    status: 'sample',
    summary: 'Push-style mission telemetry and daemon alerts for browser demos.',
    endpoints: [
      {
        method: 'WS',
        path: '/socket',
        purpose: 'Subscribe to mixed telemetry, launch, and daemon events.',
        sample: JSON.stringify({
          type: 'telemetry.tick',
          mission: mission.name,
          channels: [
            { channel: 'altitude', value: '408', unit: 'km', status: 'NOMINAL' },
            { channel: 'engine-status', value: 'NOMINAL', unit: '-', status: 'NOMINAL' },
          ],
        }, null, 2),
      },
      {
        method: 'WS',
        path: '/socket?topic=daemon',
        purpose: 'Subscribe only to background daemon lifecycle events.',
        sample: JSON.stringify({
          type: 'daemon.status',
          daemon: DAEMONS[0]?.id ?? 'funding-secured',
          status: DAEMONS[0]?.status ?? 'RUNNING',
        }, null, 2),
      },
    ],
  },
  {
    id: 'mcp',
    name: 'spaced MCP Server',
    kind: 'mcp',
    transport: 'MCP over stdio or streamable HTTP',
    status: 'sample',
    summary: 'Tool/resource surface for agents that need mission and fleet context.',
    endpoints: [
      {
        method: 'tool',
        path: 'spaced.mission.list',
        purpose: 'Return mission summaries with outcome and vehicle metadata.',
        sample: JSON.stringify({ missions: MISSIONS.map(m => ({ name: m.name, outcome: m.outcome })) }, null, 2),
      },
      {
        method: 'tool',
        path: 'spaced.telemetry.get',
        purpose: 'Return the same synthetic telemetry as the CLI command.',
        sample: JSON.stringify({ mission: mission.name, altitude_km: 408, velocity_ms: 2760 }, null, 2),
      },
      {
        method: 'resource',
        path: 'spaced://fleet/vehicles',
        purpose: 'Expose fleet inventory as an MCP resource.',
        sample: JSON.stringify({ vehicles: VEHICLES.map(v => v.name) }, null, 2),
      },
    ],
  },
  {
    id: 'events',
    name: 'Launch Event Stream',
    kind: 'events',
    transport: 'Server-sent events',
    status: 'sample',
    summary: 'Adjacent event endpoint for launch progress and archive updates.',
    endpoints: [
      {
        method: 'GET',
        path: '/events',
        purpose: 'Stream launch, telemetry, and daemon event envelopes.',
        sample: [
          'event: launch.progress',
          'data: {"mission":"Starman","phase":"coast","status":"NOMINAL"}',
        ].join('\n'),
      },
    ],
  },
];

export function findServiceSurface(id: string): ServiceSurface | undefined {
  const needle = id.toLowerCase();
  return SERVICE_SURFACES.find(s =>
    s.id === needle || s.kind === needle || s.name.toLowerCase().includes(needle),
  );
}

export function runServiceSmoke(): { surface: string; ok: boolean; detail: string }[] {
  return SERVICE_SURFACES.map(surface => ({
    surface: surface.id,
    ok: surface.endpoints.length > 0 && surface.endpoints.every(e => e.sample.length > 0),
    detail: `${surface.endpoints.length} endpoint sample(s)`,
  }));
}

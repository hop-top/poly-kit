import { describe, it, expect } from 'vitest';
import { withProvenance, type Provenance } from './provenance';

describe('withProvenance', () => {
  it('wraps data with _meta provenance', () => {
    const data = [{ id: 1, name: 'alpha' }];
    const prov: Provenance = {
      source: 'spaced',
      timestamp: '2026-04-17T12:00:00Z',
      method: 'mission.list',
    };

    const result = withProvenance(data, prov);

    expect(result.data).toBe(data);
    expect(result._meta).toEqual(prov);
  });

  it('preserves original data by reference', () => {
    const data = { key: 'value' };
    const prov: Provenance = {
      source: 'cli',
      timestamp: '2026-04-17T00:00:00Z',
      method: 'get',
    };

    const result = withProvenance(data, prov);
    expect(result.data).toBe(data);
  });

  it('timestamp is valid ISO 8601', () => {
    const prov: Provenance = {
      source: 'test',
      timestamp: '2026-04-17T12:30:45.123Z',
      method: 'list',
    };

    const result = withProvenance({}, prov);
    const parsed = new Date(result._meta.timestamp);
    expect(parsed.toISOString()).toBe('2026-04-17T12:30:45.123Z');
    expect(isNaN(parsed.getTime())).toBe(false);
  });

  it('serializes cleanly to JSON', () => {
    const result = withProvenance(
      [{ id: 1 }],
      {
        source: 'spaced',
        timestamp: '2026-04-17T00:00:00Z',
        method: 'mission.list',
      },
    );

    const json = JSON.parse(JSON.stringify(result));
    expect(json.data).toEqual([{ id: 1 }]);
    expect(json._meta.source).toBe('spaced');
    expect(json._meta.method).toBe('mission.list');
  });
});

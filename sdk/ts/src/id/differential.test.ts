// Differential test: the SDK's self-contained TypeID against typeid-js,
// the reference implementation (a dev dependency only). Random UUIDs
// and prefixes for the happy path; a fixed list of malformed inputs
// both must reject.

import { randomUUID } from 'node:crypto';

import { describe, expect, it } from 'vitest';
import { TypeID } from 'typeid-js';

import { fromUUID, newId, parse } from './index';

const PREFIXES = ['', 'a', 'task', 'my_app', 'a__b', 'a_b_c', 'z'.repeat(63)];

describe('differential vs typeid-js', () => {
  it('encodes and decodes like typeid-js for random UUIDs', () => {
    for (let i = 0; i < 500; i++) {
      const uuid = randomUUID();
      const prefix = PREFIXES[i % PREFIXES.length]!;
      const ours = fromUUID(prefix, uuid);
      expect(ours).toBe(TypeID.fromUUID(prefix, uuid).toString());
      const ref = TypeID.fromString(ours);
      expect(parse(ours)).toEqual({ prefix: ref.getType(), uuid: ref.toUUID() });
    }
  });

  it('mints UUIDv7 ids typeid-js accepts', () => {
    for (const prefix of PREFIXES) {
      const id = newId(prefix);
      const { uuid } = parse(id);
      expect(uuid[14]).toBe('7');
      expect('89ab').toContain(uuid[19]);
      expect(TypeID.fromString(id).toString()).toBe(id);
    }
  });

  it.each([
    'not-a-typeid',
    'task_too_short',
    '_01jg000000e008000000000000',
    'Task_01jg000000e008000000000000',
    'task__01jg000000e008000000000000',
    '_task_01jg000000e008000000000000',
    'task_81jg000000e008000000000000',
    'task_01JG000000E008000000000000',
    'task_01jg000000e00800000000000u',
    'task_01jg000000e0080000000000000',
    `${'a'.repeat(64)}_01jg000000e008000000000000`,
    'task1_01jg000000e008000000000000',
  ])('rejects %s like typeid-js', (s) => {
    expect(() => TypeID.fromString(s)).toThrow();
    expect(() => parse(s)).toThrow();
  });
});

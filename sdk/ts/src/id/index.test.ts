import { describe, it, expect } from 'vitest';
import { TypeID } from 'typeid-js';
import {
  newId,
  parse,
  newTyped,
  parseTyped,
  typeIdSchema,
  type Typed,
} from './index';

// Canonical fixture set — these EXACT uuid inputs are shared with the
// other language SDKs so future cross-language parity work (T-0753)
// finds matching outputs. Do not mutate without coordinating across
// sdk/{go,rs,py,php}.
const FIXTURES: ReadonlyArray<{ prefix: string; uuid: string }> = [
  { prefix: 'task', uuid: '01940000-0000-7000-8000-000000000000' },
  { prefix: 'invoice', uuid: '01940000-0000-7000-8000-000000000001' },
  { prefix: 'user', uuid: '01940000-0000-7000-8000-0000000000ff' },
];

describe('newId', () => {
  it('emits canonical `<prefix>_<26-char-base32>` form', () => {
    const id = newId('task');
    expect(id).toMatch(/^task_[0-7][0-9a-hjkmnp-tv-z]{25}$/);
  });

  it('round-trips through parse', () => {
    const id = newId('user');
    const parsed = parse(id);
    expect(parsed.prefix).toBe('user');
    expect(parsed.uuid).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it('generates distinct values across calls', () => {
    const a = newId('task');
    const b = newId('task');
    expect(a).not.toBe(b);
  });
});

describe('parse', () => {
  it.each(FIXTURES)(
    'returns matching prefix+uuid for canonical fixture %o',
    ({ prefix, uuid }) => {
      // Use typeid-js's fromUUID to compute the expected canonical
      // string; that's the cross-language oracle.
      const expected = TypeID.fromUUID(prefix, uuid).toString();
      const result = parse(expected);
      expect(result.prefix).toBe(prefix);
      expect(result.uuid).toBe(uuid);
    },
  );

  it('throws on malformed input', () => {
    expect(() => parse('not-a-typeid')).toThrow();
    expect(() => parse('task_too_short')).toThrow();
  });
});

describe('newTyped', () => {
  it('preserves the literal prefix in the runtime string', () => {
    const id = newTyped('task');
    expect(id.startsWith('task_')).toBe(true);
  });

  it('return type is assignable to Typed<P>', () => {
    // Compile-time check: the assignment below must type-check.
    const id: Typed<'task'> = newTyped('task');
    expect(typeof id).toBe('string');
  });
});

describe('parseTyped', () => {
  it('returns the input when prefix matches', () => {
    const generated = newId('task');
    const typed = parseTyped('task', generated);
    expect(typed).toBe(generated);
  });

  it('throws when prefix mismatches', () => {
    const generated = newId('task');
    expect(() => parseTyped('invoice', generated)).toThrow();
  });

  it('throws on malformed input', () => {
    expect(() => parseTyped('task', 'task_nope')).toThrow();
  });
});

describe('cross-language fixtures', () => {
  // Lock in the exact canonical strings so a regression in either
  // typeid-js or our wrapper is caught immediately. These values are
  // produced by typeid-js v1 from the fixture UUIDs; the other-language
  // SDKs must produce the byte-identical strings.
  it.each(FIXTURES)(
    'produces canonical form matching TypeID.fromUUID(%o)',
    ({ prefix, uuid }) => {
      const canonical = TypeID.fromUUID(prefix, uuid).toString();
      // structural sanity
      expect(canonical.startsWith(`${prefix}_`)).toBe(true);
      // round-trip through our parse()
      const parsed = parse(canonical);
      expect(parsed).toEqual({ prefix, uuid });
      // round-trip through our parseTyped()
      const typed = parseTyped(prefix, canonical);
      expect(typed).toBe(canonical);
    },
  );
});

describe('typeIdSchema', () => {
  it('accepts canonical typeids for the matching prefix', () => {
    const schema = typeIdSchema('task');
    const id = newId('task');
    expect(() => schema.parse(id)).not.toThrow();
  });

  it('rejects typeids with a different prefix', () => {
    const schema = typeIdSchema('task');
    const id = newId('invoice');
    expect(() => schema.parse(id)).toThrow();
  });

  it('rejects malformed suffixes', () => {
    const schema = typeIdSchema('task');
    expect(() => schema.parse('task_TOO_SHORT')).toThrow();
    expect(() => schema.parse('task_zzzzzzzzzzzzzzzzzzzzzzzzzz')).toThrow();
    // Crockford forbids i, l, o, u inside suffix
    expect(() =>
      schema.parse('task_8iiiiiiiiiiiiiiiiiiiiiiiii'),
    ).toThrow();
  });

  it('rejects non-strings', () => {
    const schema = typeIdSchema('task');
    expect(() => schema.parse(123 as unknown as string)).toThrow();
    expect(() => schema.parse(null as unknown as string)).toThrow();
  });

  it.each(FIXTURES)(
    'accepts the canonical fixture for %o',
    ({ prefix, uuid }) => {
      const schema = typeIdSchema(prefix);
      const canonical = TypeID.fromUUID(prefix, uuid).toString();
      expect(schema.parse(canonical)).toBe(canonical);
    },
  );

  it('escapes regex metacharacters in the prefix safely', () => {
    // The TypeID spec restricts prefixes to [a-z0-9_], so this is
    // defensive only; nevertheless we don't want a future relaxation
    // to make the schema crash or match too broadly.
    const schema = typeIdSchema('a.b');
    expect(() => schema.parse('axb_01h2e8kqv8000000000000000')).toThrow();
  });
});

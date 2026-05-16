import { describe, it, expect, vi, afterEach } from 'vitest'
import { open } from './sqlstore'

describe('sqlstore', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('put/get roundtrip', () => {
    it('stores and retrieves a string value', () => {
      const store = open(':memory:')
      store.put('hello', 'world')
      expect(store.get<string>('hello')).toBe('world')
      store.close()
    })

    it('stores and retrieves an object value', () => {
      const store = open(':memory:')
      store.put('obj', { foo: 1, bar: true })
      expect(store.get<{ foo: number; bar: boolean }>('obj')).toEqual({ foo: 1, bar: true })
      store.close()
    })

    it('stores and retrieves a number value', () => {
      const store = open(':memory:')
      store.put('num', 42)
      expect(store.get<number>('num')).toBe(42)
      store.close()
    })

    it('overwrites an existing key', () => {
      const store = open(':memory:')
      store.put('k', 'first')
      store.put('k', 'second')
      expect(store.get<string>('k')).toBe('second')
      store.close()
    })
  })

  describe('missing key', () => {
    it('returns null for a key that does not exist', () => {
      const store = open(':memory:')
      expect(store.get('missing')).toBeNull()
      store.close()
    })
  })

  describe('TTL expiry', () => {
    it('returns value when not yet expired', () => {
      const now = 1_000_000
      vi.spyOn(Date, 'now').mockReturnValue(now)
      const store = open(':memory:', { ttl: 5000 })
      store.put('ttl-key', 'alive')
      // still within TTL
      vi.spyOn(Date, 'now').mockReturnValue(now + 4999)
      expect(store.get<string>('ttl-key')).toBe('alive')
      store.close()
    })

    it('returns null after TTL has elapsed', () => {
      const now = 1_000_000
      vi.spyOn(Date, 'now').mockReturnValue(now)
      const store = open(':memory:', { ttl: 5000 })
      store.put('ttl-key', 'doomed')
      // advance past TTL
      vi.spyOn(Date, 'now').mockReturnValue(now + 5001)
      expect(store.get('ttl-key')).toBeNull()
      store.close()
    })

    it('returns value when TTL is not set', () => {
      const store = open(':memory:')
      store.put('no-ttl', 'persists')
      expect(store.get<string>('no-ttl')).toBe('persists')
      store.close()
    })
  })

  describe('migrateSql', () => {
    it('runs migrateSql on open and makes table available', () => {
      const store = open(':memory:', {
        migrateSql: 'CREATE TABLE IF NOT EXISTS meta (id INTEGER PRIMARY KEY, name TEXT)',
      })
      const db = store.db()
      // If table exists, this insert should succeed without throwing
      expect(() => db.prepare('INSERT INTO meta (name) VALUES (?)').run('test')).not.toThrow()
      store.close()
    })
  })

  describe('close', () => {
    it('close() makes subsequent operations throw', () => {
      const store = open(':memory:')
      store.put('x', 1)
      store.close()
      expect(() => store.get('x')).toThrow()
    })
  })

  describe('db()', () => {
    it('returns a Database instance', () => {
      const store = open(':memory:')
      const db = store.db()
      expect(db).toBeDefined()
      // Database has a prepare method
      expect(typeof db.prepare).toBe('function')
      store.close()
    })
  })
})

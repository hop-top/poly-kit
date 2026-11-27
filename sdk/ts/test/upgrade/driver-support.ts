// Shared by the driver tests: a Migrator over a failing second migration,
// and a hand-built tar.gz writer for archives `tar` itself would not make.

import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';

import * as tar from 'tar';

/** Makes a temp dir; the returned function removes it. */
export function tempDir(prefix = 'upgrade-driver-'): { dir: string; cleanup: () => void } {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  return { dir, cleanup: () => rmSync(dir, { recursive: true, force: true }) };
}

export interface RawMember {
  path: string;
  type?: 'File' | 'Directory' | 'SymbolicLink' | 'Link' | 'FIFO' | 'CharacterDevice';
  body?: string;
  linkpath?: string;
}

/** Writes a gzip'd tar of exactly these members, names unchecked. */
export function writeRawTarGz(file: string, members: RawMember[]): void {
  const blocks: Buffer[] = [];
  for (const m of members) {
    const body = Buffer.from(m.body ?? '');
    const head = Buffer.alloc(512);
    new tar.Header({
      path: m.path,
      type: m.type ?? 'File',
      mode: 0o644,
      size: body.length,
      linkpath: m.linkpath,
    }).encode(head, 0);
    blocks.push(head);
    if (body.length > 0) {
      blocks.push(body, Buffer.alloc((512 - (body.length % 512)) % 512));
    }
  }
  blocks.push(Buffer.alloc(1024));
  writeFileSync(file, gzipSync(Buffer.concat(blocks)));
}

/**
 * @module index
 * @package @hop-top/kit
 *
 * Root barrel: namespace re-exports of every public subpath, so
 * `require('@hop-top/kit')` / `import ... from '@hop-top/kit'` resolve.
 * Namespaces mirror the package exports map one-to-one.
 *
 * `sqlstore` is intentionally NOT re-exported here: an eager re-export
 * would load better-sqlite3 native bindings on every bare import of the
 * package. Use the dedicated subpath instead: `@hop-top/kit/sqlstore`.
 */

export * as aim from './aim.js';
export * as alias from './alias.js';
export * as api from './api.js';
export * as auth from './auth.js';
export * as cli from './cli.js';
export * as config from './config.js';
export * as errcorrect from './errcorrect.js';
export * as id from './id/index.js';
export * as llm from './llm.js';
export * as mcp from './mcp/index.js';
export * as netpolicy from './netpolicy.js';
export * as output from './output.js';
export * as progress from './progress.js';
export * as provenance from './provenance.js';
export * as routellm from './routellm.js';
export * as rpc from './rpc.js';
export * as safety from './safety.js';
export * as scope from './scope.js';
export * as stream from './stream.js';
export * as telemetry from './telemetry/index.js';
export * as tui from './tui/index.js';
export * as upgrade from './upgrade.js';
export * as xdg from './xdg.js';

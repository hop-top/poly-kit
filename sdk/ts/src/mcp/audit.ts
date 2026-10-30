/**
 * @module mcp/audit
 * @package @hop-top/kit
 *
 * The refusal audit hook: the counterpart, for the MCP surface, of the
 * bridge sinks Go's `cmdsurface` fans every refusal out to. Go audits
 * each refusal the surface makes before adopter code runs (`Bridge.Audit`
 * from `mcpsdk`'s gates and the bridge's own `refuse`); this port has no
 * sink registry, so one mount-level hook receives the same record.
 *
 * Contract, shared by every kit SDK: the hook is called synchronously
 * after the refusal is decided and before the response is returned (Go's
 * order: audit, then respond); it is best-effort, so a throw is swallowed
 * and the wire response is never affected; and the `error` field carries
 * Go's sentinel message verbatim, so adopters forwarding to a backend
 * that already receives Go's sinks see one vocabulary.
 */

import type { McpAuditHook, McpAuditRecord } from './types.js';

/** `cmdsurface.ErrAuthRefused`: a `kit/auth-required` leaf, no established caller. */
export const MCP_AUDIT_AUTH_REFUSED = 'cmdsurface: authentication refused';

/** `mcpsdk.ErrConfirmationRequired`: nothing satisfied the confirmation gate. */
export const MCP_AUDIT_CONFIRMATION_REQUIRED = 'mcpsdk: confirmation required';

/** `mcpsdk.ErrConfirmationDeclined`: the client's user declined or dismissed the question. */
export const MCP_AUDIT_CONFIRMATION_DECLINED = 'mcpsdk: confirmation declined';

/** `mcpsdk.ErrConfirmationStateRejected`: a presented `requestState` failed verification. */
export const MCP_AUDIT_CONFIRMATION_STATE_REJECTED =
  'mcpsdk: confirmation requestState failed verification';

/** `cmdsurface.ErrDestructiveBlocked`: the leaf's destructiveness exceeds the policy ceiling. */
export const MCP_AUDIT_DESTRUCTIVE_BLOCKED =
  'cmdsurface: destructive command blocked on this surface';

/** The `mcp_confirm_rejection` value a rejected `requestState` carries, as in Go. */
export const MCP_CONFIRM_REJECTION_STATE = 'request_state_verification_failed';

/**
 * Emits one refusal record to `hook`, when there is one. Builds the
 * record here so every call site carries the same shape: the surface
 * pinned to `mcp`, the time the refusal was decided, and `extra`
 * always carrying `mcp_spec_version` ahead of any event-specific key.
 * A throwing hook is swallowed: audit must never turn a refusal into
 * a different refusal.
 */
export function auditRefusal(
  hook: McpAuditHook | undefined,
  path: string[],
  specVersion: string,
  error: string,
  extra?: Record<string, string>,
): void {
  if (hook === undefined) return;
  const record: McpAuditRecord = {
    path: [...path],
    surface: 'mcp',
    requestedAt: new Date(),
    specVersion,
    error,
    extra: { mcp_spec_version: specVersion, ...(extra ?? {}) },
  };
  try {
    hook(record);
  } catch {
    // Best-effort by contract: the response is already decided.
  }
}

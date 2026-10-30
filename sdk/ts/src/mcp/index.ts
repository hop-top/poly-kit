/**
 * @module mcp
 * @package @hop-top/kit
 *
 * Dual-spec MCP surface: one mount serving both 2024-11-05 (legacy
 * handshake) and 2026-07-28 (stateless per-request envelope), with
 * per-request era detection.
 *
 * The protocol layer comes from the official v2 SDK packages
 * (`@modelcontextprotocol/core` + `@modelcontextprotocol/server`).
 * The v1 package `@modelcontextprotocol/sdk` is legacy-era only and
 * cannot serve 2026-07-28 — it is deliberately not a dependency.
 *
 * Usage:
 *
 * ```ts
 * import { createMcpHandler } from '@hop-top/kit/mcp';
 *
 * const handler = createMcpHandler(bridge);
 * const res = await handler({ method: 'POST', headers, body });
 * ```
 *
 * The handler is framework-free: binding it to node:http, hono,
 * express, fastify, or a Worker is the adopter's job. For a host that
 * spawns the tool, `serveStdio(bridge)` (or `serveStdio(handler)`)
 * serves the same surface over newline-delimited stdin/stdout.
 */

export {
  createMcpHandler,
  detectMcpEra,
  hasModernMetaMarker,
  mcpMountConfig,
  McpDispatcher,
  methodNotAllowedResponse,
  parseJsonRpcRequest,
  type JsonRpcRequest,
  type McpEra,
  type McpHandler,
  type McpHttpRequest,
  type McpHttpResponse,
  type McpTransport,
} from './dispatch.js';

export { LegacyMcpHandler } from './legacy.js';

export {
  auditRefusal,
  MCP_AUDIT_AUTH_REFUSED,
  MCP_AUDIT_CONFIRMATION_DECLINED,
  MCP_AUDIT_CONFIRMATION_REQUIRED,
  MCP_AUDIT_CONFIRMATION_STATE_REJECTED,
  MCP_AUDIT_DESTRUCTIVE_BLOCKED,
  MCP_CONFIRM_REJECTION_STATE,
} from './audit.js';

export {
  serveStdio,
  type McpStdioOptions,
  type McpStdioServer,
} from './stdio.js';

export {
  decodeMcpSentinel,
  ModernMcpHandler,
  modernInvocationExtra,
  parseModernMeta,
  rawToolCallName,
  singleHeaderValue,
  validModernRequestId,
  type ModernRequestMeta,
} from './modern.js';

export {
  clientSupportsFormElicitation,
  ElicitationConfirmGate,
  headerConfirmationGate,
  MCP_CONFIRM_KEY,
  MCP_CONFIRM_STATE_TTL_SECONDS,
  MCP_CONFIRM_STATE_VERSION,
  mcpConfirmArgsDigest,
  mcpConfirmPrincipal,
  mintMcpConfirmState,
  parseMcpConfirmRetry,
  verifyMcpConfirmState,
  type McpConfirmBinding,
  type McpConfirmDecision,
  type McpConfirmRejection,
  type McpConfirmStateStatus,
} from './modern-confirm.js';

export {
  discoverCapabilities,
  isTaskMethod,
  TASK_METHODS,
  TASKS_EXTENSION,
  TASKS_SUPPORTED,
  type TaskMethod,
} from './tasks.js';

export {
  buildToolEnvelope,
  canonicalJSONStringify,
  DEFAULT_MCP_PATH,
  DEFAULT_MCP_SERVER_NAME,
  DEFAULT_MCP_SERVER_VERSION,
  DEFAULT_TOOL_NAME_SEPARATOR,
  defaultPolicy,
  DestructiveBlockedError,
  encodeEnvelope,
  errorResultBlock,
  HEADER_MCP_METHOD,
  HEADER_MCP_NAME,
  HEADER_MCP_PROTOCOL_VERSION,
  MCP_ERR_HEADER_MISMATCH,
  MCP_ERR_INTERNAL,
  MCP_ERR_INVALID_PARAMS,
  MCP_ERR_INVALID_REQUEST,
  MCP_ERR_METHOD_NOT_FOUND,
  MCP_ERR_MISSING_CLIENT_CAPABILITY,
  MCP_ERR_PARSE,
  MCP_ERR_UNSUPPORTED_VERSION,
  MCP_SPEC_2024_11_05,
  MCP_SPEC_2026_07_28,
  META_KEY_CLIENT_CAPABILITIES,
  META_KEY_CLIENT_INFO,
  META_KEY_PROTOCOL_VERSION,
  META_KEY_SERVER_INFO,
  MCP_AUTH_CHALLENGE,
  pathFromToolName,
  policyAllowed,
  RawJSON,
  renderCallResult,
  resolveMcpConfig,
  RESULT_TYPE_COMPLETE,
  RESULT_TYPE_INPUT_REQUIRED,
  SURFACE_CLI,
  SURFACE_LIB,
  SURFACE_MCP,
  SurfaceNotEnabledError,
  toolName,
  UnknownCommandError,
  type FlagSchema,
  type Invocation,
  type InvocationMeta,
  type InvokeResult,
  type JsonRpcErrorObject,
  type Leaf,
  type LeafArg,
  type McpAuditHook,
  type McpAuditRecord,
  type McpBridge,
  type McpCacheScope,
  type McpEstablishment,
  type McpIdentity,
  type McpMountOptions,
  type McpSpecVersion,
  type McpVerifier,
  type Policy,
  type ResolvedMcpConfig,
  type SafetyClass,
  type Surface,
} from './types.js';

export { establishCaller, invocationMeta, SCOPES_EXTRA_KEY } from './identity.js';

export { commanderBridge, type CommanderBridgeOptions } from './bridge.js';

export {
  MCP_ARGS_PROPERTY,
  mcpArgsPublished,
  mcpInputSchema,
  mcpSplitArguments,
  mcpToolDescription,
  type McpSplitResult,
} from './args.js';

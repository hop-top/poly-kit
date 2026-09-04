/**
 * @module mcp/tasks
 * @package @hop-top/kit
 *
 * The `io.modelcontextprotocol/tasks` extension slot.
 *
 * kit does not implement the tasks extension (ADR 0042 gap matrix:
 * "Not implemented. capabilities.extensions omitted from
 * server/discover (= unsupported); tasks/* → -32601 @ 404"). This
 * module makes that decision explicit and testable rather than
 * implicit in a `default:` branch, and marks the designated slot if
 * the extension is ever added.
 *
 * The negotiation contract matters for parity: because
 * server/discover omits `capabilities.extensions` entirely, a
 * conforming client never negotiates the extension and never sends
 * `tasks/*`. A client that sends it anyway gets the same
 * method-not-found response any other unknown modern method gets —
 * NOT a distinct "extension unsupported" error, which would be a wire
 * behavior the Go reference does not produce.
 */

import { MCP_ERR_METHOD_NOT_FOUND } from './types.js';

/** The reserved extension identifier, as named by the spec. */
export const TASKS_EXTENSION = 'io.modelcontextprotocol/tasks';

/**
 * The task-extension methods. Listed so the surface can recognize
 * them for documentation and testing purposes; recognizing one does
 * NOT change its response.
 */
export const TASK_METHODS = [
  'tasks/get',
  'tasks/update',
  'tasks/list',
  'tasks/result',
  'tasks/cancel',
] as const;

export type TaskMethod = (typeof TASK_METHODS)[number];

/** Reports whether a method name belongs to the tasks extension. */
export function isTaskMethod(method: string): method is TaskMethod {
  return (TASK_METHODS as readonly string[]).includes(method);
}

/**
 * Whether this mount supports the tasks extension. Always false: the
 * extension is a non-goal, and `server/discover` therefore omits the
 * `capabilities.extensions` map entirely (an empty map would
 * advertise the *ability* to carry extensions, which is a different
 * claim than making none).
 */
export const TASKS_SUPPORTED = false as const;

/**
 * The response for an unsupported tasks method: identical to the
 * generic modern method-not-found (-32601 @ 404). Kept as a named
 * function so the "tasks are unsupported" decision has one call site
 * a reviewer can find, and so a future implementation replaces this
 * rather than editing a catch-all branch.
 */
export function taskMethodNotFound(method: string): {
  code: number;
  msg: string;
  status: number;
} {
  return {
    code: MCP_ERR_METHOD_NOT_FOUND,
    msg: `method not found: ${method}`,
    status: 404,
  };
}

/**
 * The `capabilities` object `server/discover` reports. The extensions
 * map is omitted rather than emitted empty — see TASKS_SUPPORTED.
 */
export function discoverCapabilities(): Record<string, unknown> {
  return { tools: {} };
}

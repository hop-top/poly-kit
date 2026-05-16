/**
 * Thin facade over @hop-top/uri for kit SDK consumers.
 *
 * The URI implementation lives in @hop-top/uri; this module only adapts the
 * kit-facing helper names used by the Go URI command group.
 */

export interface URI {
  readonly scheme: string;
  readonly namespace: string;
  readonly id: string;
  readonly query: string;
  readonly fragment: string;
  readonly original: string;
  readonly action: string;
  canonical(): string;
  vanity(): string;
  toString(): string;
}

export interface VanityAlias {
  from: string;
  to: string;
  prefix?: boolean;
  preserveSuffix?: boolean;
}

export interface ActionRoute {
  command: string;
  args?: string[];
}

export interface ResolvedAction {
  action: string;
  command: string;
  args: string[];
}

export interface Policy {
  defaultNamespaceSegments?: number;
  schemeNamespaceSegments?: Record<string, number>;
  vanityAliases?: VanityAlias[];
  actionRoutes?: Record<string, ActionRoute>;
}

export interface ParseOptions {
  strict?: boolean;
  jsonAmbiguity?: boolean;
}

export interface VanityCandidate {
  from: string;
  to: string;
  distance: number;
}

export type Parser = (input: string) => URI;
export type Completer = (prefix: string) => string[] | Promise<string[]>;

export interface TypeRegistration {
  name: string;
  parser?: Parser;
  completer?: Completer;
}

export interface Registry {
  register(reg: TypeRegistration): void;
  parse(input: string): URI;
  completeVanity(input: string): VanityCandidate[];
  complete(typeName: string, prefix: string): Promise<string[]>;
  types(): string[];
}

export interface CompletionResult {
  suggestions: string[];
}

export type Language = 'go' | 'ts' | 'py' | 'rs' | 'php';

export interface HandlerSpec {
  vendor: string;
  app: string;
  instance?: string;
  language: Language | string;
  scheme: string;
  version?: string;
  channel?: string;
  appPath: string;
  displayName?: string;
}

export type HandlerPlatform = 'linux' | 'macos' | 'ios' | 'windows' | string;

interface UriModule {
  defaultPolicy: Policy;
  DefaultPolicy?: Policy;
  parse(input: string, policy?: Policy, options?: ParseOptions): URI;
  resolveAction(uri: URI, policy?: Policy): ResolvedAction;
  vanityCandidates(input: string, policy?: Policy): VanityCandidate[];
  newRegistry(policy?: Policy): Registry;
  Registry: new (policy?: Policy) => Registry;
  completeWithScheme(
    registry: Registry,
    typeName: string,
    toComplete: string,
  ): Promise<CompletionResult>;
  validateHandlerSpec(spec: HandlerSpec): void;
  handlerID(spec: HandlerSpec): string;
  desktopFilename(spec: HandlerSpec): string;
  snippet(platform: string, spec: HandlerSpec): string;
}

// @hop-top/uri is intentionally a runtime dependency of the facade. Keeping the
// import as require avoids duplicating its declarations in this package.
// eslint-disable-next-line @typescript-eslint/no-require-imports
const uri = require('@hop-top/uri') as UriModule;

export const defaultPolicy: Policy = uri.defaultPolicy;
export const DefaultPolicy: Policy = uri.DefaultPolicy ?? uri.defaultPolicy;
export const Registry: new (policy?: Policy) => Registry = uri.Registry;

/** Parse a custom URI using @hop-top/uri policy and parse options. */
export function parse(
  input: string,
  policy?: Policy,
  options?: ParseOptions,
): URI {
  return uri.parse(input, policy, options);
}

/**
 * Resolve an action route to a command plan without executing it.
 * Accepts either a parsed URI or a raw URI string.
 */
export function resolve(
  input: string | URI,
  policy?: Policy,
  options?: ParseOptions,
): ResolvedAction {
  const parsed = typeof input === 'string' ? uri.parse(input, policy, options) : input;
  return uri.resolveAction(parsed, policy);
}

/** Resolve an already parsed URI action route without executing it. */
export function resolveAction(parsed: URI, policy?: Policy): ResolvedAction {
  return uri.resolveAction(parsed, policy);
}

/** Return vanity alias completion candidates. */
export function completeVanity(
  input: string,
  policy?: Policy,
): VanityCandidate[] {
  return uri.vanityCandidates(input, policy);
}

export const vanityCandidates = completeVanity;

/** Complete URI values using a @hop-top/uri registry. */
export function complete(
  registry: Registry,
  typeName: string,
  input: string,
): Promise<CompletionResult> {
  return uri.completeWithScheme(registry, typeName, input);
}

/** Create a @hop-top/uri registry with an optional policy. */
export function newRegistry(policy?: Policy): Registry {
  return uri.newRegistry(policy);
}

export function validateHandlerSpec(spec: HandlerSpec): void {
  uri.validateHandlerSpec(spec);
}

export function handlerID(spec: HandlerSpec): string {
  return uri.handlerID(spec);
}

export function desktopFilename(spec: HandlerSpec): string {
  return uri.desktopFilename(spec);
}

export function handlerSnippet(
  platform: HandlerPlatform,
  spec: HandlerSpec,
): string {
  return uri.snippet(platform, spec);
}

export const snippet = handlerSnippet;

export const handler = {
  validate: validateHandlerSpec,
  id: handlerID,
  desktopFilename,
  snippet: handlerSnippet,
};

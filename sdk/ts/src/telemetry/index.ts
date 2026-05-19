/**
 * @module telemetry
 * @package @hop-top/kit
 *
 * SDK-side telemetry primitives — TS mirror of
 * `go/runtime/telemetry` (Mode + install_id) and the read path of
 * `go/core/consent` (consent file reader).
 *
 * This module covers the three building blocks SDK consumers need
 * before composing an emitter:
 *
 *   - `Mode` / `parseMode` / `resolveMode`   — what tier do we emit?
 *   - `getInstallId` / `rotate` / `installIdPath` — anonymous installation id.
 *   - `loadConsent` / `consentPath` / `Consent` — has the operator opted in?
 *
 * The emit path itself, the BatchProcessor, and the OTLP exporter land
 * in follow-up sdk-telemetry tasks.
 *
 * Ground truth + decisions:
 *   - ADR-0035 
 *   - ADR-0036 
 *   - ADR-0038 
 *   - Event-schema doc `sdk/docs/telemetry-event-schema.md`
 */

export { Mode, parseMode, resolveMode } from './mode';
export {
  getInstallId,
  rotate,
  installIdPath,
  resetForTest,
} from './installId';
export {
  loadConsent,
  consentPath,
  deniedConsent,
  type Consent,
} from './consent';
export { redact, redactString } from './redact';
export {
  Client,
  type ClientOptions,
  type SinkKind,
  type Envelope,
} from './client';

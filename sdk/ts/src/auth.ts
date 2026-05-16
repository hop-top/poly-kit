/**
 * @module auth
 * @package @hop-top/kit
 *
 * Factor 12 — Auth and Credential Lifecycle.
 *
 * Provides introspection and refresh for credentials.
 * NoAuth is the zero-value implementation.
 */

export interface Credential {
  source: string;
  identity: string;
  scopes: string[];
  expiresAt?: string;
  renewable: boolean;
}

export interface AuthIntrospector {
  inspect(): Promise<Credential>;
  refresh(): Promise<void>;
}

export class NoAuth implements AuthIntrospector {
  async inspect(): Promise<Credential> {
    return {
      source: 'none',
      identity: 'anonymous',
      scopes: [],
      renewable: false,
    };
  }

  async refresh(): Promise<void> {
    // no-op
  }
}

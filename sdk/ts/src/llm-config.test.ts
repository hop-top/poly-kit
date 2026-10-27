/**
 * llm.yaml, key resolution order and the key-endpoint guard of the
 * core: loadConfig, applyAPIKey, credentialsTrusted. Go `llm`
 * (LoadConfig, ApplyAPIKey, CredentialsTrusted) is canonical; the
 * llm-v1 vectors pin the endpoint rules, these the layering.
 */

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  KeyEndpointError,
  LLMError,
  MissingKeyError,
  applyAPIKey,
  clearRegistry,
  credentialsTrusted,
  loadConfig,
  register,
  type Provider,
} from "./llm";
import { isolateEnv } from "../test/llm/conformance-helpers";

const ENV = [
  "LLM_API_KEY", "LLM_BASE_URL", "LLM_PROVIDER", "LLM_FALLBACK",
  "KITA_API_KEY", "KITA_ALT_KEY", "KITB_API_KEY", "KITC_API_KEY", "NOSUCH_API_KEY",
];

let restore: () => void;
beforeEach(() => {
  restore = isolateEnv(ENV);
  register("kita", stub, { endpoints: ["https://api.kita.test"] });
  register("kitb", stub, { key: { envVars: [], optional: true } });
  register("kitc", stub, { key: { envVars: ["KITC_API_KEY"], optional: true } });
});
afterEach(() => {
  restore();
  clearRegistry();
  writeYaml("");
});

function stub(): Provider {
  return { close: () => undefined };
}

/** Writes llm.yaml where the core reads it; "" removes its content. */
function writeYaml(yaml: string): void {
  const dir = join(process.env["XDG_CONFIG_HOME"]!, "hop");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "llm.yaml"), yaml);
}

describe("key resolution order", () => {
  it("URI api_key, then llm.yaml api_key, api_key_env, <SCHEME>_API_KEY, LLM_API_KEY", () => {
    process.env["LLM_API_KEY"] = "k-fallback";
    expect(loadConfig("kita://m").provider.apiKey).toBe("k-fallback");
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(loadConfig("kita://m").provider.apiKey).toBe("k-scheme");
    process.env["KITA_ALT_KEY"] = "k-alt";
    writeYaml("providers:\n  kita:\n    api_key_env: KITA_ALT_KEY\n");
    expect(loadConfig("kita://m").provider.apiKey).toBe("k-alt");
    writeYaml("providers:\n  kita:\n    api_key: k-file\n    api_key_env: KITA_ALT_KEY\n");
    expect(loadConfig("kita://m").provider.apiKey).toBe("k-file");
    expect(loadConfig("kita://m?api_key=k-uri").provider.apiKey).toBe("k-uri");
  });

  it("treats a blank value as unset at every source", () => {
    process.env["KITA_API_KEY"] = "k-scheme";
    writeYaml("providers:\n  kita:\n    api_key: '  '\n");
    const cfg = loadConfig("kita://m?api_key= ");
    expect(cfg.provider.apiKey).toBe("k-scheme");
    expect(cfg.uri.params).toBeUndefined();
  });

  it("lends LLM_API_KEY only to a scheme whose adapter requires a key", () => {
    process.env["LLM_API_KEY"] = "k-fallback";
    process.env["KITB_API_KEY"] = "k-b";
    expect(loadConfig("kitb://m").provider.apiKey).toBeUndefined();
    expect(loadConfig("kitc://m").provider.apiKey).toBeUndefined();
    process.env["KITC_API_KEY"] = "k-c";
    expect(loadConfig("kitc://m").provider.apiKey).toBe("k-c");
    expect(loadConfig("nosuch://m").provider.apiKey).toBeUndefined();
    process.env["NOSUCH_API_KEY"] = "k-nosuch";
    expect(loadConfig("nosuch://m").provider.apiKey).toBeUndefined();
    writeYaml("providers:\n  nosuch:\n    api_key_env: NOSUCH_KEY_VAR\n");
    expect(loadConfig("nosuch://m").provider.apiKey).toBeUndefined();
    // A scheme no adapter serves still takes its own llm.yaml key.
    writeYaml("providers:\n  nosuch:\n    api_key: k-own\n");
    expect(loadConfig("nosuch://m").provider.apiKey).toBe("k-own");
  });
});

describe("llm.yaml", () => {
  it("supplies default, fallback, model, base_url and extras", () => {
    writeYaml(
      [
        "default: kita://",
        "fallback: [kitb://x]",
        "providers:",
        "  kita:",
        "    model: m-file",
        "    base_url: https://api.kita.test",
        "    tier: gold",
        "",
      ].join("\n")
    );
    const cfg = loadConfig();
    expect(cfg.uri.scheme).toBe("kita");
    expect(cfg.provider.model).toBe("m-file");
    expect(cfg.provider.baseURL).toBe("https://api.kita.test");
    expect(cfg.provider.extras).toEqual({ tier: "gold" });
    expect(cfg.fallbacks).toEqual(["kitb://x"]);
    expect(loadConfig("kita://m-uri").provider.model).toBe("m-uri");
  });

  it("ranks the URI over LLM_BASE_URL over llm.yaml for the base URL", () => {
    writeYaml("providers:\n  kita:\n    base_url: https://file.test\n");
    expect(loadConfig("kita://m").provider.baseURL).toBe("https://file.test");
    process.env["LLM_BASE_URL"] = "https://env.test";
    expect(loadConfig("kita://m").provider.baseURL).toBe("https://env.test");
    expect(loadConfig("kita://m?base_url=https://uri.test").provider.baseURL).toBe(
      "https://uri.test"
    );
  });

  it("LLM_PROVIDER and LLM_FALLBACK outrank llm.yaml", () => {
    writeYaml("default: kita://file\nfallback: [kita://fb-file]\n");
    process.env["LLM_PROVIDER"] = "kita://env";
    process.env["LLM_FALLBACK"] = "kita://fb-env";
    const cfg = loadConfig();
    expect(cfg.uri.model).toBe("env");
    expect(cfg.fallbacks).toEqual(["kita://fb-env"]);
  });

  it("is ignored when it does not parse", () => {
    writeYaml("providers: [unclosed\n");
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(loadConfig("kita://m").provider.apiKey).toBe("k-scheme");
  });

  it("is not read by a hermetic config", () => {
    writeYaml(
      "default: kita://file\nproviders:\n  kita:\n    api_key: k-file\n    base_url: https://file.test\n"
    );
    expect(() => loadConfig("", { hermetic: true })).toThrow(LLMError);
    const cfg = loadConfig("kita://m", { hermetic: true });
    expect(cfg.provider.apiKey).toBeUndefined();
    expect(cfg.provider.baseURL).toBeUndefined();
    expect(cfg.provider.extras).toBeUndefined();
  });

  it("base_url makes an endpoint trusted for kit's own credentials", () => {
    expect(credentialsTrusted("kita", "https://gw.corp.test/v1")).toBe(false);
    writeYaml("providers:\n  kita:\n    base_url: https://gw.corp.test\n");
    expect(credentialsTrusted("kita", "https://gw.corp.test/v1")).toBe(true);
    expect(credentialsTrusted("kitb", "https://gw.corp.test/v1")).toBe(false);
  });
});

describe("key-endpoint guard at load", () => {
  it("keeps a key kit resolved off an endpoint only the URI names", () => {
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(() => loadConfig("kita://m?base_url=https://evil.test")).toThrow(KeyEndpointError);
    expect(() => loadConfig("kita://m?base_url=https://api.kita.test/v2")).not.toThrow();
    expect(() => loadConfig("kita://m?api_key=k-uri&base_url=https://evil.test")).not.toThrow();
  });

  it("refuses any key over plain http to a remote host", () => {
    expect(() => loadConfig("kita://m?api_key=k-uri&base_url=http://evil.test")).toThrow(
      KeyEndpointError
    );
    process.env["KITA_API_KEY"] = "k-scheme";
    process.env["LLM_BASE_URL"] = "http://gw.corp.test";
    expect(() => loadConfig("kita://m")).toThrow(KeyEndpointError);
  });

  it("names llm.yaml base_url in the refusal", () => {
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(() => loadConfig("kita://m?base_url=https://evil.test")).toThrow(
      'llm: refusing to send the kita API key to "https://evil.test", which only the URI names; ' +
        "set it as llm.yaml base_url or LLM_BASE_URL, or pass api_key in the URI"
    );
  });
});

describe("applyAPIKey", () => {
  it("appends the resolved key", () => {
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(applyAPIKey("kita://m")).toBe("kita://m?api_key=k-scheme");
    expect(applyAPIKey("kita://m?t=1")).toBe("kita://m?t=1&api_key=k-scheme");
  });

  it("keeps the URI's own key and drops a blank one", () => {
    process.env["KITA_API_KEY"] = "k-scheme";
    expect(applyAPIKey("kita://m?api_key=k-uri")).toBe("kita://m?api_key=k-uri");
    expect(applyAPIKey("kita://m?api_key=&t=1")).toBe("kita://m?t=1&api_key=k-scheme");
  });

  it("returns the URI unchanged when the scheme takes no key", () => {
    process.env["LLM_API_KEY"] = "k-fallback";
    expect(applyAPIKey("kitb://m")).toBe("kitb://m");
    expect(applyAPIKey("nosuch://m")).toBe("nosuch://m");
  });

  it("throws MissingKeyError for a required key found nowhere", () => {
    let err: unknown;
    try {
      applyAPIKey("kita://m");
    } catch (e) {
      err = e;
    }
    expect(err).toBeInstanceOf(MissingKeyError);
    expect((err as MissingKeyError).envVars).toEqual(["KITA_API_KEY", "LLM_API_KEY"]);
    expect((err as Error).message).toBe(
      'llm: no API key for provider "kita" (model "m"): set KITA_API_KEY or LLM_API_KEY'
    );
  });

  it("refuses a key a URI cannot carry", () => {
    process.env["KITA_API_KEY"] = "k&x";
    expect(() => applyAPIKey("kita://m")).toThrow(
      "llm: kita API key contains a character a provider URI cannot carry (& ? #)"
    );
  });

  it("never quotes its input on a parse error", () => {
    expect(() => applyAPIKey("kita/m?api_key=secret-value")).toThrow(
      "llm: apply API key: provider URI must be scheme://model"
    );
  });
});

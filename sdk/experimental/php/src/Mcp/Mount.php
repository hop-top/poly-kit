<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Mount configuration for the MCP surface.
 *
 * Mirrors Go's `MountMCP` options. The option *set* is normative across
 * every port; only the spelling is language-idiomatic.
 *
 * Mount-time errors are reproduced rather than silently absorbed,
 * because a mount that quietly serves nothing — accepts a nonsense cache
 * hint, or names a tool that does not map back to one leaf — is worse
 * than one that refuses to start.
 */
final readonly class Mount
{
    /**
     * `$verifier` establishes the caller of each `tools/call`: it checks a
     * credential the request presents and returns the Identity, or null
     * to refuse (throwing refuses too). Without one no call is
     * established, so every `kit/auth-required` leaf is refused with 401:
     * an `Authorization` header alone never admits one.
     *
     * `$toolNameSeparator` (default ".") joins a leaf path into its tool
     * name and splits a called name back: "_" names the leaf `widget add`
     * `widget_add`, for hosts that admit only `^[A-Za-z0-9_-]{1,64}$`. An
     * empty separator is refused here; any separator but the default that
     * a command name contains is refused by {@see self::dispatcher()},
     * since the name would not map back to one leaf.
     *
     * `$audit` receives one {@see AuditRecord} per refusal the surface
     * makes before adopter code runs — the parity hook for Go's bridge
     * sinks. It is called after the refusal is decided and before the
     * response is returned, and it is best-effort: a hook that throws
     * changes nothing on the wire.
     *
     * @param list<SpecVersion>                   $specVersions      revisions this mount serves
     * @param list<string>                        $originAllowlist   allowed `Origin` values; empty disables the check
     * @param (\Closure(Request): ?Identity)|null $verifier          establishes the caller of each `tools/call`
     * @param string                              $toolNameSeparator joins a leaf path into its tool name
     * @param (\Closure(AuditRecord): void)|null  $audit             observes each pre-flight refusal
     */
    public function __construct(
        public string $path = '/mcp',
        public ServerInfo $serverInfo = new ServerInfo(),
        public array $specVersions = [SpecVersion::Legacy, SpecVersion::Modern],
        public CacheHints $cacheHints = new CacheHints(),
        public array $originAllowlist = [],
        public ?string $confirmationKey = null,
        public ?\Closure $verifier = null,
        public string $toolNameSeparator = Leaf::DEFAULT_TOOL_NAME_SEPARATOR,
        public ?\Closure $audit = null,
    ) {
        if ([] === $specVersions) {
            throw new \InvalidArgumentException(
                'cmdsurface: WithMCPSpecVersions: at least one spec version required',
            );
        }

        if ('' === $toolNameSeparator) {
            throw new \InvalidArgumentException('cmdsurface: WithToolNameSeparator: empty separator');
        }
    }

    /**
     * Refuses, for any separator but the default, a leaf whose command
     * name contains it: `data_sync` and `data sync` both name `data_sync`
     * under "_", so the mount refuses rather than guessing.
     */
    private function checkToolNameSeparator(Bridge $bridge): void
    {
        if (Leaf::DEFAULT_TOOL_NAME_SEPARATOR === $this->toolNameSeparator) {
            return;
        }

        foreach ($bridge->leaves() as $leaf) {
            foreach ($leaf->path as $segment) {
                if (str_contains($segment, $this->toolNameSeparator)) {
                    throw new \InvalidArgumentException(\sprintf(
                        'cmdsurface: WithToolNameSeparator: command %s in %s contains separator %s',
                        GoFormat::quote($segment),
                        GoFormat::quote($leaf->pathKey()),
                        GoFormat::quote($this->toolNameSeparator),
                    ));
                }
            }
        }
    }

    /**
     * The confirmation strategy for this mount.
     *
     * A configured key opts into the MRTR elicitation flow; without one
     * the surface keeps the `X-Confirm-Token` header check.
     */
    private function confirmationGate(Auditor $auditor): ConfirmationGate
    {
        $header = new HeaderConfirmationGate($auditor);

        if (null === $this->confirmationKey) {
            return $header;
        }

        return new ElicitationConfirmationGate(
            new ConfirmationState($this->confirmationKey),
            fallback: $header,
            toolNameSeparator: $this->toolNameSeparator,
            auditor: $auditor,
        );
    }

    public function serves(SpecVersion $version): bool
    {
        return \in_array($version, $this->specVersions, true);
    }

    /**
     * Builds the dispatcher for this mount over $bridge.
     *
     * $transport selects the binding: {@see Transport::Stdio} has no
     * header layer, so the 2026-07-28 header checks (V4, V6, V7) do not
     * apply there; every other check runs unchanged.
     *
     * @throws \InvalidArgumentException when a command name contains a non-default tool-name separator
     */
    public function dispatcher(Bridge $bridge, Transport $transport = Transport::Http): Dispatcher
    {
        $this->checkToolNameSeparator($bridge);

        $auditor = new Auditor($this->audit);

        return new Dispatcher(
            legacy: new LegacyHandler(
                $bridge,
                $this->serverInfo,
                $this->verifier,
                $this->toolNameSeparator,
                $auditor,
            ),
            modern: new ModernHandler(
                $bridge,
                $this->serverInfo,
                $this->cacheHints,
                $this->originAllowlist,
                $this->confirmationGate($auditor),
                $this->verifier,
                $this->toolNameSeparator,
                $transport,
                $auditor,
            ),
            legacyEnabled: $this->serves(SpecVersion::Legacy),
            modernEnabled: $this->serves(SpecVersion::Modern),
        );
    }
}

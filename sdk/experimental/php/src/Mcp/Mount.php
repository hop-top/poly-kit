<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Mount configuration for the MCP surface.
 *
 * Mirrors Go's `MountMCP` options. The option *set* is normative across
 * every port; only the spelling is language-idiomatic.
 *
 * Two mount-time errors are reproduced rather than silently absorbed,
 * because a mount that quietly serves nothing — or accepts a nonsense
 * cache hint — is worse than one that refuses to start.
 */
final readonly class Mount
{
    /**
     * @param list<SpecVersion> $specVersions    revisions this mount serves
     * @param list<string>      $originAllowlist allowed `Origin` values; empty disables the check
     */
    public function __construct(
        public string $path = '/mcp',
        public ServerInfo $serverInfo = new ServerInfo(),
        public array $specVersions = [SpecVersion::Legacy, SpecVersion::Modern],
        public CacheHints $cacheHints = new CacheHints(),
        public array $originAllowlist = [],
        public ?string $confirmationKey = null,
    ) {
        if ([] === $specVersions) {
            throw new \InvalidArgumentException(
                'cmdsurface: WithMCPSpecVersions: at least one spec version required',
            );
        }
    }

    /**
     * The confirmation strategy for this mount.
     *
     * A configured key opts into the MRTR elicitation flow; without one
     * the surface keeps the `X-Confirm-Token` header check.
     */
    private function confirmationGate(): ConfirmationGate
    {
        if (null === $this->confirmationKey) {
            return new HeaderConfirmationGate();
        }

        return new ElicitationConfirmationGate(new ConfirmationState($this->confirmationKey));
    }

    public function serves(SpecVersion $version): bool
    {
        return \in_array($version, $this->specVersions, true);
    }

    /** Builds the dispatcher for this mount over $bridge. */
    public function dispatcher(Bridge $bridge): Dispatcher
    {
        return new Dispatcher(
            legacy: new LegacyHandler($bridge, $this->serverInfo),
            modern: new ModernHandler(
                $bridge,
                $this->serverInfo,
                $this->cacheHints,
                $this->originAllowlist,
                $this->confirmationGate(),
            ),
            legacyEnabled: $this->serves(SpecVersion::Legacy),
            modernEnabled: $this->serves(SpecVersion::Modern),
        );
    }
}

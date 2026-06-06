<?php

declare(strict_types=1);

namespace HopTop\Kit\Uri;

use Hop\Cite\ParseOptions;
use Hop\Cite\Policy;
use Hop\Cite\ResolvedAction;
use Hop\Cite\Handle;
use Hop\Cite\HandlerSpec;
use Hop\Cite\Scheme;
use Hop\Cite\URI as ParsedUri;

final class UriFacade
{
    public static function defaultPolicy(): Policy
    {
        return Scheme::defaultPolicy();
    }

    public static function parse(string $input, ?Policy $policy = null, ?ParseOptions $options = null): ParsedUri
    {
        return Scheme::parse($input, $policy, $options);
    }

    public static function canonical(ParsedUri $uri): string
    {
        return $uri->canonical();
    }

    public static function vanity(ParsedUri $uri): string
    {
        return $uri->vanity();
    }

    public static function resolveAction(ParsedUri $uri, Policy $policy): ResolvedAction
    {
        return $policy->resolveAction($uri);
    }

    public static function handlerId(HandlerSpec $spec): string
    {
        return $spec->handlerId();
    }

    public static function handlerSnippet(string $platform, HandlerSpec $spec): string
    {
        return Handle::snippet($platform, $spec);
    }

    public static function handlerDesktopFilename(HandlerSpec $spec): string
    {
        return Handle::desktopFilename($spec);
    }
}

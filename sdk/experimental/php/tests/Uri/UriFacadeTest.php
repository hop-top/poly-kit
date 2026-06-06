<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Uri;

use Hop\Cite\ActionRoute;
use Hop\Cite\HandlerSpec;
use Hop\Cite\Policy;
use HopTop\Kit\Uri\UriFacade;
use PHPUnit\Framework\TestCase;

final class UriFacadeTest extends TestCase
{
    public function testParseDelegatesToHopUriScheme(): void
    {
        $uri = UriFacade::parse('task://hop-top/uri/T-0001');

        $this->assertSame('task', $uri->scheme);
        $this->assertSame('hop-top/uri', $uri->namespace);
        $this->assertSame('T-0001', $uri->id);
        $this->assertSame('task://hop-top/uri/T-0001', UriFacade::canonical($uri));
    }

    public function testResolveActionDelegatesToPolicy(): void
    {
        $policy = new Policy(
            defaultNamespaceSegments: 1,
            schemeNamespaceSegments: ['tlc' => 2],
            actionRoutes: [
                'task.claim' => new ActionRoute(
                    command: 'tlc',
                    args: ['-C', '{namespace}', 'task', 'claim', '{id}'],
                ),
            ],
        );

        $uri = UriFacade::parse('tlc://org/repo/T-0001?action=task.claim', $policy);
        $plan = UriFacade::resolveAction($uri, $policy);

        $this->assertSame('task.claim', $plan->action);
        $this->assertSame('tlc', $plan->command);
        $this->assertSame(['-C', 'org/repo', 'task', 'claim', 'T-0001'], $plan->args);
    }

    public function testHandlerHelpersDelegateToHopUriHandle(): void
    {
        $spec = new HandlerSpec(
            vendor: 'hop-top',
            app: 'tlc',
            language: HandlerSpec::LANGUAGE_PHP,
            scheme: 'tlc',
            appPath: '/usr/local/bin/tlc',
        );

        $this->assertSame('hop-top.tlc.php.tlc', UriFacade::handlerId($spec));
        $this->assertSame('hop-top.tlc.php.tlc.desktop', UriFacade::handlerDesktopFilename($spec));
        $this->assertStringContainsString(
            'X-Hop-Handler-ID=hop-top.tlc.php.tlc',
            UriFacade::handlerSnippet('linux', $spec),
        );
    }
}

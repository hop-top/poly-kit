<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry;

use HopTop\Kit\Telemetry\Redactor;
use PHPUnit\Framework\TestCase;

class RedactorTest extends TestCase
{
    private string|false $prevHome;

    protected function setUp(): void
    {
        $this->prevHome = $_SERVER['HOME'] ?? false;
        $_SERVER['HOME'] = '/home/alice';
    }

    protected function tearDown(): void
    {
        if ($this->prevHome === false) {
            unset($_SERVER['HOME']);
        } else {
            $_SERVER['HOME'] = $this->prevHome;
        }
    }

    public function testRedactsEmail(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'contact <redacted:email> please',
            $r->redact('contact alice@example.com please'),
        );
    }

    public function testRedactsIpv4(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'from <redacted:ipv4> port 443',
            $r->redact('from 10.0.0.1 port 443'),
        );
    }

    public function testRedactsIpv6(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'host <redacted:ipv6>',
            $r->redact('host 2001:db8::1'),
        );
    }

    public function testRedactsSkToken(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'auth <redacted:token>',
            $r->redact('auth sk-abcd1234EFGH5678'),
        );
    }

    public function testRedactsGithubToken(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'token <redacted:token>',
            $r->redact('token ghp_abcdefghij0123456789ABCDEF'),
        );
    }

    public function testRedactsSlackBotToken(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'slack <redacted:token>',
            $r->redact('slack xoxb-1234-5678-abcdEFGHijklMNOPqrstUVWX'),
        );
    }

    public function testReplacesHomePrefix(): void
    {
        $r = new Redactor();
        $this->assertSame(
            'cd $HOME/code',
            $r->redact('cd /home/alice/code'),
        );
    }

    public function testRedactsRecursivelyInArrays(): void
    {
        $r = new Redactor();
        $in = [
            'user' => 'alice@example.com',
            'nested' => [
                'ip' => '192.168.1.1',
                'home' => '/home/alice/.cache',
            ],
            'count' => 7,
            'flag' => true,
            'nope' => null,
        ];
        $out = $r->redact($in);

        $this->assertSame('<redacted:email>', $out['user']);
        $this->assertSame('<redacted:ipv4>', $out['nested']['ip']);
        $this->assertSame('$HOME/.cache', $out['nested']['home']);
        $this->assertSame(7, $out['count']);
        $this->assertTrue($out['flag']);
        $this->assertNull($out['nope']);
    }

    public function testLeavesNonStringScalarsUnchanged(): void
    {
        $r = new Redactor();
        $this->assertSame(42, $r->redact(42));
        $this->assertSame(3.14, $r->redact(3.14));
        $this->assertTrue($r->redact(true));
        $this->assertNull($r->redact(null));
    }

    public function testCustomCallbackRunsAfterDefault(): void
    {
        $r = new Redactor(function (array $attrs): array {
            // The default has already replaced alice@example.com.
            if (is_array($attrs) && isset($attrs['user']) && $attrs['user'] === '<redacted:email>') {
                $attrs['user'] = '<redacted:custom-email>';
            }

            return $attrs;
        });

        $out = $r->redactAttrs(['user' => 'alice@example.com']);
        $this->assertSame('<redacted:custom-email>', $out['user']);
    }

    public function testCustomCallbackOutputReRedacted(): void
    {
        // Custom callback re-introduces a token shape; default pass
        // should catch it.
        $r = new Redactor(function (array $attrs): array {
            $attrs['leaked'] = 'sk-zzzzzzzzzzzz';

            return $attrs;
        });
        $out = $r->redactAttrs([]);
        $this->assertSame('<redacted:token>', $out['leaked']);
    }

    public function testMisbehavingCustomCallbackCoercedToArray(): void
    {
        $r = new Redactor(fn (array $_attrs): string => 'not-an-array');
        $this->assertSame([], $r->redactAttrs(['user' => 'a@b.com']));
    }

    public function testEmptyHomeIsNotReplaced(): void
    {
        unset($_SERVER['HOME']);
        putenv('HOME=');
        $r = new Redactor();
        // No accidental wildcard substitution.
        $this->assertSame('plain text', $r->redact('plain text'));
    }
}

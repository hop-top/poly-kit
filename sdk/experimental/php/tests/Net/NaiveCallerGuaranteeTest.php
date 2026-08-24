<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use HopTop\Kit\Api\ApiClient;
use HopTop\Kit\Net\NetPolicy;
use HopTop\Kit\Net\OfflineException;
use PHPUnit\Framework\TestCase;

/**
 * The guarantee test. Port of the Go end-to-end
 * TestOfflineIsEnforcedForNaiveLeaf: a caller who never heard of the
 * offline marker — no check, no injected client, just the default
 * construction path — must still be refused.
 *
 * Marker-only enforcement would pass every test in NetPolicyTest and
 * still fail this one, which is the point: an advisory flag any caller
 * can forget to consult does not keep the promise the parity guide
 * makes for `--offline`.
 */
class NaiveCallerGuaranteeTest extends TestCase
{
    protected function setUp(): void
    {
        NetPolicy::reset();
    }

    protected function tearDown(): void
    {
        NetPolicy::reset();
    }

    /**
     * ApiClient's zero-arg construction path (`?? new Client()`) is the
     * naive case: an adopter who passes no client of their own. It must
     * inherit the guard without asking for it.
     */
    public function testNaiveApiClientIsRefusedWhenOffline(): void
    {
        NetPolicy::setOffline(true);

        // No injected http client: exactly what a naive adopter writes.
        $api = new ApiClient('https://api.example.invalid');

        $this->expectException(OfflineException::class);
        $api->get('abc');
    }

    /**
     * Same naive construction, loopback target: `--offline` means "do
     * not talk to the network", not "do not talk to myself", so a local
     * dev backend stays reachable. The connection is refused by the OS
     * rather than by the guard, which is what we assert: any exception
     * raised here must NOT be an OfflineException.
     */
    public function testNaiveApiClientReachesLoopbackWhenOffline(): void
    {
        NetPolicy::setOffline(true);

        $api = new ApiClient('http://127.0.0.1:1/');

        try {
            $api->get('abc');
        } catch (OfflineException $e) {
            $this->fail('guard blocked loopback: ' . $e->getMessage());
        } catch (\Throwable) {
            // Connection refused / transport error: the guard let it
            // through and the OS declined. That is the expected path.
            $this->addToAssertionCount(1);
        }
    }

    /** With the marker clear, the naive path is untouched. */
    public function testNaiveApiClientIsNotBlockedWhenOnline(): void
    {
        $api = new ApiClient('http://127.0.0.1:1/');

        try {
            $api->get('abc');
        } catch (OfflineException $e) {
            $this->fail('guard blocked while online: ' . $e->getMessage());
        } catch (\Throwable) {
            $this->addToAssertionCount(1);
        }
    }
}

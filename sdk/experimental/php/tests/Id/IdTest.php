<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Id;

use HopTop\Kit\Id\Id;
use HopTop\Kit\Id\IdException;
use HopTop\Kit\Id\InvalidPrefixException;
use HopTop\Kit\Id\InvalidSuffixException;
use HopTop\Kit\Id\ParsedId;
use HopTop\Kit\Id\PrefixMismatchException;
use HopTop\Kit\Id\Typed;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

/**
 * Canonical fixtures: same UUIDv7 inputs used across the cross-language
 * parity work (T-0753). DO NOT change the UUID strings — Go, Rust, TS,
 * Python, and PHP must all produce identical output suffixes from them.
 */
final class IdTest extends TestCase
{
    /**
     * @return iterable<string, array{0: string, 1: string}>
     */
    public static function canonicalFixtures(): iterable
    {
        yield 'task'    => ['task',    '01940000-0000-7000-8000-000000000000'];
        yield 'invoice' => ['invoice', '01940000-0000-7000-8000-000000000001'];
        yield 'user'    => ['user',    '01940000-0000-7000-8000-0000000000ff'];
    }

    #[DataProvider('canonicalFixtures')]
    public function testFromUuidRoundTrip(string $prefix, string $uuid): void
    {
        $id = Id::fromUuid($prefix, $uuid);

        // Canonical wire form: <prefix>_<26-char suffix>.
        $this->assertStringStartsWith($prefix.'_', $id);
        $this->assertSame(26, strlen(substr($id, strlen($prefix) + 1)));

        // Parse must recover the original prefix + uuid bit-for-bit.
        $parsed = Id::parse($id);
        $this->assertInstanceOf(ParsedId::class, $parsed);
        $this->assertSame($prefix, $parsed->prefix);
        $this->assertSame($uuid, $parsed->uuid);
    }

    public function testNewGeneratesValidParseableId(): void
    {
        $id = Id::new('user');

        $this->assertMatchesRegularExpression('/^user_[0-9a-z]{26}$/', $id);

        $parsed = Id::parse($id);
        $this->assertSame('user', $parsed->prefix);
        // UUIDv7: version nibble at hex[12] must be '7'.
        $this->assertSame('7', $parsed->uuid[14]);
    }

    public function testNewAcceptsEmptyPrefix(): void
    {
        $id = Id::new('');
        $this->assertMatchesRegularExpression('/^[0-9a-z]{26}$/', $id);

        $parsed = Id::parse($id);
        $this->assertSame('', $parsed->prefix);
    }

    public function testInvalidPrefixRaisesInvalidPrefixException(): void
    {
        $this->expectException(InvalidPrefixException::class);
        // Uppercase is rejected by TypeID v0.3 grammar.
        Id::new('BadPrefix');
    }

    public function testInvalidPrefixIsAlsoIdException(): void
    {
        try {
            Id::new('Bad');
            $this->fail('expected InvalidPrefixException');
        } catch (IdException $e) {
            $this->assertInstanceOf(InvalidPrefixException::class, $e);
        }
    }

    public function testParseRejectsInvalidSuffix(): void
    {
        $this->expectException(IdException::class);
        // Wrong length & invalid Crockford chars.
        Id::parse('task_THIS_IS_NOT_A_VALID_SUFFIX');
    }

    public function testParseRejectsEmptyString(): void
    {
        $this->expectException(IdException::class);
        Id::parse('');
    }

    public function testFromUuidRejectsBadUuid(): void
    {
        $this->expectException(InvalidSuffixException::class);
        Id::fromUuid('user', 'not-a-uuid');
    }

    public function testJsonRoundTripOnTypedWrapper(): void
    {
        $task = TaskId::generate();
        $encoded = json_encode($task);
        $this->assertIsString($encoded);

        // Wire form is the bare typeid string, JSON-encoded as a quoted string.
        $this->assertSame(json_encode((string) $task), $encoded);

        // Round-trip: decode → parse → equal value.
        $decoded = json_decode($encoded, true);
        $this->assertIsString($decoded);
        $recovered = TaskId::parse($decoded);
        $this->assertSame((string) $task, (string) $recovered);
    }

    public function testJsonRoundTripFromCanonicalFixture(): void
    {
        $id = Id::fromUuid('task', '01940000-0000-7000-8000-000000000000');
        $task = TaskId::parse($id);

        $encoded = json_encode(['id' => $task]);
        $this->assertIsString($encoded);

        $payload = json_decode($encoded, true);
        $this->assertIsArray($payload);
        $this->assertSame($id, $payload['id']);
    }

    public function testParseTypedRejectsPrefixMismatch(): void
    {
        $invoiceId = Id::fromUuid('invoice', '01940000-0000-7000-8000-000000000001');

        $this->expectException(PrefixMismatchException::class);
        TaskId::parse($invoiceId);
    }

    public function testTypedGenerateUsesDeclaredPrefix(): void
    {
        $task = TaskId::generate();
        $this->assertStringStartsWith('task_', (string) $task);

        // The same generated value parses cleanly through the typed wrapper.
        $recovered = TaskId::parse((string) $task);
        $this->assertSame((string) $task, (string) $recovered);
    }

    public function testTypedToStringMatchesValue(): void
    {
        $id = Id::fromUuid('task', '01940000-0000-7000-8000-000000000000');
        $task = TaskId::parse($id);

        $this->assertSame($id, (string) $task);
        $this->assertSame($id, $task->value);
    }
}

/**
 * Concrete `Typed` subclass kept colocated with the test that exercises it —
 * no other module needs `TaskId` yet, so we don't bake it into the SDK.
 */
final class TaskId extends Typed
{
    protected const string PREFIX = 'task';
}

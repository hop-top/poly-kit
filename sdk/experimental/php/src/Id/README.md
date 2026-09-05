# Id

## What it answers

How are identifiers minted and parsed so every port agrees on the TypeID prefix grammar and suffix
encoding? `Id` is a static facade over `jewei/typeid-php` (Jetify TypeID v0.3); `Typed` binds a
prefix to a PHP class. Composing an id into a `scheme://namespace/id` URI is `HopTop\Kit\Uri`, not
here. Telemetry's `install_id` is a SHA-256 digest, not a TypeID.

## Use it when

- you create a new entity id: `Id::new('task')`
- you need the prefix and UUID back out of a string: `Id::parse($s)`
- a fixture must be reproducible across languages: `Id::fromUuid($prefix, $uuid)`
- a function must refuse ids of the wrong kind: subclass `Typed` with a `PREFIX` constant

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Id\Id;
use HopTop\Kit\Id\PrefixMismatchException;
use HopTop\Kit\Id\Typed;

final class TaskId extends Typed
{
    protected const string PREFIX = 'task';
}

$id = Id::new('task');                 // task_01k... (fresh UUIDv7)
$parsed = Id::parse($id);
var_dump($parsed->prefix === 'task', Id::fromUuid('task', $parsed->uuid) === $id); // true, true

// Deterministic vector from contracts/typeid-v1/fixtures.json
echo Id::fromUuid('task', '01940000-0000-7000-8000-000000000000'), "\n";
// task_01jg000000e008000000000000

$task = TaskId::generate();
echo json_encode(['task' => $task]), "\n"; // {"task":"task_01k..."}
try {
    TaskId::parse('invoice_01jg000000e008000000000000');
} catch (PrefixMismatchException $e) {
    echo $e->getMessage(), "\n"; // expected prefix "task", got "invoice" in "invoice_01jg..."
}
```

## Contract

- Prefix grammar: `^([a-z]([a-z_]{0,61}[a-z])?)?$`, at most 63 chars, empty allowed (bare id);
  violations throw `InvalidPrefixException`, suffix problems throw `InvalidSuffixException`
- `ParsedId::$uuid` is the hyphenated UUIDv7; `Id::new($p)` then `Id::parse()` round-trips exactly
- `Typed` serialises to the bare canonical string via `__toString()` and `jsonSerialize()`
- Parity: [`contracts/typeid-v1/fixtures.json`](../../../../../contracts/typeid-v1/fixtures.json),
  replayed by `tests/Id/ContractTest.php`

## Neighbours

- `HopTop\Kit\Uri`: URIs whose `id` segment is one of these strings
- `HopTop\Kit\Telemetry`: `InstallId`, a different identifier with a different shape

## See also

- [Go reference: `go/core/id`](../../../../../go/core/id/)

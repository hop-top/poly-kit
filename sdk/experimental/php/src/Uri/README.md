# Uri

## What it answers

How does a kit caller parse, canonicalise and route the shared `scheme://namespace/id` URI contract
without depending on parsing code of its own? `UriFacade` delegates every call to `hop-top/cite`
(`^0.2.0`) and adds nothing. Identifiers inside the URI are minted by `HopTop\Kit\Id`, not here.

## Use it when

- you receive a URI string and need its scheme, namespace and id: `UriFacade::parse()`
- you print a URI back out: `UriFacade::canonical()` or `UriFacade::vanity()`
- a `?action=` URI must become a command plan: `UriFacade::resolveAction()` with a `Policy`
- you register a desktop handler: `handlerId()`, `handlerSnippet()`, `handlerDesktopFilename()`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Cite\ActionRoute;
use HopTop\Cite\Policy;
use HopTop\Kit\Uri\UriFacade;

$uri = UriFacade::parse('task://acme/app/42');
echo $uri->namespace, ' ', UriFacade::canonical($uri), "\n"; // acme/app task://acme/app/42

$policy = new Policy(
    defaultNamespaceSegments: 1,
    schemeNamespaceSegments: ['tlc' => 2],
    actionRoutes: ['task.claim' => new ActionRoute(command: 'tlc', args: ['-C', '{namespace}', 'task', 'claim', '{id}'])],
);
$plan = UriFacade::resolveAction(UriFacade::parse('tlc://org/repo/42?action=task.claim', $policy), $policy);
echo $plan->command, ' ', implode(' ', $plan->args), "\n"; // tlc -C org/repo task claim 42
```

## Contract

- `parse()` without a policy uses `Scheme::defaultPolicy()` from `hop-top/cite`; namespace segment
  counts and action routes come from the policy you pass, never from kit
- Return types are `hop-top/cite` types (`URI`, `ResolvedAction`, `HandlerSpec`); the facade owns no
  types of its own
- Parity: none recorded; Go `go/console/uri` is the reference

## Neighbours

- `HopTop\Kit\Id`: the TypeID strings that appear as the `id` segment
- `HopTop\Kit\Cli`: where a `--uri` argument would be resolved into a command

## See also

- [URI facade](../../../../../docs/adopters/reference/php-sdk.md#uri-facade)
- [Go reference: `go/console/uri`](../../../../../go/console/uri/)

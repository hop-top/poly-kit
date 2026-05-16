# php

experimental PHP client SDK.

## URI facade

The experimental SDK exposes a thin facade over `hop-top/uri` so kit callers can
use the shared URI contract without depending on kit-specific parsing code.

```php
<?php

use Hop\Uri\ActionRoute;
use Hop\Uri\Policy;
use HopTop\Kit\Uri\UriFacade;

$uri = UriFacade::parse('task://hop-top/uri/T-0001');
echo $uri->namespace; // hop-top/uri
echo UriFacade::canonical($uri); // task://hop-top/uri/T-0001

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

$actionUri = UriFacade::parse('tlc://org/repo/T-0001?action=task.claim', $policy);
$plan = UriFacade::resolveAction($actionUri, $policy);
```

This facade intentionally delegates to `hop-top/uri`; it does not reimplement
URI parsing, vanity handling, action routing, or handler identity.

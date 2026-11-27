<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** No step leaves the document's current version before the latest. */
final class DocGapException extends DocException {}

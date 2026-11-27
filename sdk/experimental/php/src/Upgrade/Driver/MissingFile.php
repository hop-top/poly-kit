<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use HopTop\Kit\Upgrade\UpgradeException;

/** A file a driver was asked to copy is not there. @internal */
final class MissingFile extends UpgradeException {}

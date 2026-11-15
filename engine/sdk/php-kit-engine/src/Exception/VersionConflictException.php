<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Exception;

/** 409: revert/fork/merge seq out of range, or abandon of a non-head or last live head. */
final class VersionConflictException extends HttpException
{
}

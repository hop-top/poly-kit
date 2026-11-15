<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Exception;

/** 409: requested version does not exist (revert of a missing version). */
final class VersionConflictException extends HttpException
{
}

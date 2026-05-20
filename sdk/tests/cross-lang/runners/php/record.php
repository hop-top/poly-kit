<?php
/**
 * PHP runner for the cross-language telemetry contract harness.
 *
 * Reads ``fixtures/input.json``, configures a per-runner ``JsonlSink``
 * pointed at ``KIT_TELEMETRY_SINK_FILE`` (the env name kits other SDKs
 * also honor, even though the PHP SDK's default sink derives its own
 * per-PID path), then calls ``Telemetry::record()`` + ``Telemetry::flush()``.
 *
 * Pre-conditions (orchestrator-owned):
 *   - ``XDG_STATE_HOME`` + ``XDG_CONFIG_HOME`` point into the temp dir.
 *   - install_id + consent fixtures pre-seeded under their canonical paths.
 *   - ``KIT_TELEMETRY_MODE=full``.
 *   - ``KIT_TELEMETRY_SINK_FILE`` points at a writable temp path.
 *   - ``HOME=/test/home`` (so the $HOME redactor rewrites the fixture path).
 */

declare(strict_types=1);

$here = __DIR__;
$crossLang = realpath($here . '/../..');
$phpRoot = realpath($crossLang . '/../../experimental/php');

if ($phpRoot === false) {
    fwrite(STDERR, "php runner: cannot resolve PHP SDK root\n");
    exit(2);
}

$autoload = $phpRoot . '/vendor/autoload.php';
if (!file_exists($autoload)) {
    fwrite(STDERR, "php runner: missing vendor/autoload.php at {$autoload}\n");
    fwrite(STDERR, "Run `composer install` in hops/main/sdk/experimental/php/ first.\n");
    exit(2);
}
require $autoload;

use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Telemetry;

$sinkPath = getenv('KIT_TELEMETRY_SINK_FILE');
if ($sinkPath === false || $sinkPath === '') {
    fwrite(STDERR, "php runner: KIT_TELEMETRY_SINK_FILE must be set\n");
    exit(2);
}

// Inject a deterministic sink at the harness-controlled path.
$sink = new JsonlSink(
    path: $sinkPath,
    cap: 16,
    sizeRotationBytes: 10 * 1024 * 1024,
    registerShutdown: false,
);
Telemetry::setSink($sink);

$inputPath = getenv('KIT_CROSS_LANG_INPUT');
if ($inputPath === false || $inputPath === '') {
    $inputPath = $crossLang . '/fixtures/input.json';
}
$payload = json_decode(
    (string) file_get_contents($inputPath),
    true,
);

Telemetry::record($payload['event'], $payload['attrs']);
Telemetry::flush();

exit(0);

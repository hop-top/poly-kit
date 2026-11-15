<?php

declare(strict_types=1);

// Parity runner: drives one kit serve (KIT_ENGINE_PORT / KIT_ENGINE_TOKEN)
// through the shared create/update/history/revert scenario and prints the
// normalized JSON the cross-SDK harness compares.

require __DIR__ . '/../vendor/autoload.php';

use HopTop\KitEngine\KitEngine;

$engine = KitEngine::connect((int) getenv('KIT_ENGINE_PORT'), getenv('KIT_ENGINE_TOKEN') ?: null);
$notes = $engine->collection('notes');
$created = $notes->create(['title' => 'v1']);
$id = $created['id'];
$notes->update($id, ['title' => 'v2']);
$notes->update($id, ['title' => 'v3']);
$before = $notes->history($id);
$reverted = $notes->revert($id, 1);
$after = $notes->history($id);

$keys = array_keys($created);
sort($keys);

echo json_encode([
    'createdKeys' => $keys,
    'beforeVersions' => array_column($before, 'version'),
    'beforeOps' => array_column($before, 'operation'),
    'beforeTitles' => array_map(static fn (array $v) => $v['data']['title'], $before),
    'revertedTitle' => $reverted['data']['title'],
    'afterVersions' => array_column($after, 'version'),
    'afterOps' => array_column($after, 'operation'),
    'afterTitles' => array_map(static fn (array $v) => $v['data']['title'], $after),
], JSON_THROW_ON_ERROR), "\n";

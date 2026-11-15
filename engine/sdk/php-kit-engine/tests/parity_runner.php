<?php

declare(strict_types=1);

// Parity runner: drives one kit serve (KIT_ENGINE_PORT / KIT_ENGINE_TOKEN)
// through the shared create/update/history/revert + branch-ops scenario and prints the
// normalized JSON the cross-SDK harness compares.

require __DIR__ . '/../vendor/autoload.php';

use HopTop\KitEngine\Exception\HttpException;
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

// Branch ops on their own doc; errors normalize to the HTTP status.
$status = static function (callable $fn): int {
    try {
        $fn();

        return 0;
    } catch (HttpException $e) {
        return $e->status;
    }
};
$seqs = static function (array $heads): array {
    $s = array_column($heads, 'seq');
    sort($s);

    return $s;
};
$b = $notes->create(['title' => 'b1'])['id'];
$notes->update($b, ['title' => 'b2']);
$notes->update($b, ['title' => 'b3']);
$forkA = $notes->fork($b, 2);
$headsAfterFork = $seqs($notes->branches($b));
$notes->abandon($b, $forkA['seq']);
$liveAfterAbandon = $seqs($notes->branches($b, true));
$abandonLastLive = $status(fn () => $notes->abandon($b, 3));
$forkB = $notes->fork($b, 2);
$merged = $notes->merge($b, $forkB['seq'], 3, ['title' => 'merged']);
$pruneNoPolicy = $status(fn () => $notes->prune($b));
$pruned = $notes->prune($b, maxVersions: 1);
$missing = 'no-such-doc';
$forkKeys = array_keys($forkA);
sort($forkKeys);
// The engine does not yet bridge bus events to the socket, so only the
// connect + subscribe path is observable: iteration throws if the first
// connect fails, and otherwise ends quietly at the timeout.
foreach ($engine->events('kit.engine.document.*', 0.5) as $_) {
}
$eventsOpen = true;

echo json_encode([
    'eventsOpen' => $eventsOpen,
    'forkSeq' => $forkA['seq'],
    'forkParents' => count($forkA['parent_ids']),
    'forkKeys' => $forkKeys,
    'headsAfterFork' => $headsAfterFork,
    'liveAfterAbandon' => $liveAfterAbandon,
    'abandonLastLive' => $abandonLastLive,
    'forkBSeq' => $forkB['seq'],
    'mergeSeq' => $merged['seq'],
    'mergeParents' => count($merged['parent_ids']),
    'pruneNoPolicy' => $pruneNoPolicy,
    'pruneRemoved' => count($pruned['versions_removed']),
    'pruneBlobsFreed' => $pruned['blobs_freed'],
    'pruneBytesFreed' => $pruned['bytes_freed'],
    'forkOutOfRange' => $status(fn () => $notes->fork($b, 99)),
    'mergeOutOfRange' => $status(fn () => $notes->merge($b, 99, 1, ['title' => 'x'])),
    'forkMissing' => $status(fn () => $notes->fork($missing, 1)),
    'mergeMissing' => $status(fn () => $notes->merge($missing, 2, 1, ['title' => 'x'])),
    'pruneMissing' => $status(fn () => $notes->prune($missing, maxVersions: 1)),
    'abandonMissing' => $status(fn () => $notes->abandon($missing, 1)),
    'createdKeys' => $keys,
    'beforeVersions' => array_column($before, 'version'),
    'beforeOps' => array_column($before, 'operation'),
    'beforeTitles' => array_map(static fn (array $v) => $v['data']['title'], $before),
    'revertedTitle' => $reverted['data']['title'],
    'afterVersions' => array_column($after, 'version'),
    'afterOps' => array_column($after, 'operation'),
    'afterTitles' => array_map(static fn (array $v) => $v['data']['title'], $after),
], JSON_THROW_ON_ERROR), "\n";

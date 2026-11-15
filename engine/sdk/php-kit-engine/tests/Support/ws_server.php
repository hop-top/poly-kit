<?php

declare(strict_types=1);

/**
 * Scripted /events stand-in for tests. Not a test case.
 *
 * Usage: php ws_server.php <port> <script.json> <log.jsonl>
 *
 * script: {"conns":[{"frames":[<frame>...],"drop":bool,"refuse":bool}...]}
 * Connection N uses conns[min(N, last)]. After each subscribe the server
 * acks, sends the frames, and drops the socket when "drop" is true.
 * The log gets one JSON line per handshake ({"open":{path,auth}}) and
 * per received text frame ({"recv":<frame>}), and {"gone":true} per
disconnect.
 */

require dirname(__DIR__, 2) . '/vendor/autoload.php';

use WebSocket\Connection;
use WebSocket\Message\Text;
use WebSocket\Server;

[, $port, $scriptFile, $logFile] = $argv;
$script = json_decode((string) file_get_contents($scriptFile), true, 512, JSON_THROW_ON_ERROR);
$conns = $script['conns'];
$n = 0;

$log = static function (array $row) use ($logFile): void {
    file_put_contents($logFile, json_encode($row) . "\n", FILE_APPEND | LOCK_EX);
};

$server = new Server((int) $port);
$server->setTimeout(1);
$server
    ->onHandshake(function (Server $s, Connection $c, $req, $res) use (&$n, $conns, $log): void {
        $c->setMeta('cfg', $conns[min($n, count($conns) - 1)]);
        $n++;
        $log(['open' => [
            'path' => $req->getUri()->getPath(),
            'auth' => $req->getHeaderLine('Authorization'),
        ]]);
        $c->send(new Text(json_encode(['type' => 'welcome'])));
    })
    ->onDisconnect(function () use ($log): void {
        $log(['gone' => true]);
    })
    ->onText(function (Server $s, Connection $c, Text $m) use ($log): void {
        $frame = json_decode($m->getContent(), true);
        $log(['recv' => $frame]);
        if (($frame['type'] ?? '') !== 'subscribe') {
            return;
        }
        $c->send(new Text(json_encode(['type' => 'ack', 'topic' => $frame['topic'] ?? ''])));
        $cfg = $c->getMeta('cfg');
        foreach ($cfg['frames'] ?? [] as $f) {
            $c->send(new Text(is_string($f) ? $f : json_encode($f)));
        }
        if (!empty($cfg['drop'])) {
            $c->disconnect();
        }
    })
    ->start();

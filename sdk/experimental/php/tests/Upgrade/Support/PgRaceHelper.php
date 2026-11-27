<?php

declare(strict_types=1);

// A racing process of the postgres test: `php PgRaceHelper.php <table> <schema>`; the connection
// comes from the KIT_UPGRADE_PG_* environment. Waits for a line on stdin, then migrates.

use HopTop\Kit\Upgrade\Driver\Postgres;
use HopTop\Kit\Upgrade\FsUtil;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;

require dirname(__DIR__, 3) . '/vendor/autoload.php';

$dsn = (string) getenv('KIT_UPGRADE_PG_DSN');
$connect = static fn (): PDO => new PDO($dsn, (string) getenv('KIT_UPGRADE_PG_USER'), (string) getenv('KIT_UPGRADE_PG_PASSWORD'), [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
[$table, $schema] = [$argv[1] ?? '', $argv[2] ?? ''];

$db = $connect();
$reg = new Registry();
foreach (['1.1.0', '1.2.0', '1.3.0'] as $v) {
    $reg->register(new Migration($v, $schema, static function () use ($db, $table, $v): void {
        usleep(150_000); // widen the race window
        $db->prepare("INSERT INTO {$table}_applied (version, pid) VALUES (?, ?)")->execute([$v, getmypid()]);
    }));
}
$data = sys_get_temp_dir() . '/kit-pg-race-' . getmypid();
$m = new Migrator('racetest', '1.3.0', registry: $reg, dataDir: $data);
$m->addDriver(new Postgres($db, $table, $connect, name: $schema));
fgets(STDIN);
try {
    $m->run();
} finally {
    FsUtil::removeAll($data);
}
echo "done\n";

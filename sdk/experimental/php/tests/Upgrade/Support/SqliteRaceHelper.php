<?php

declare(strict_types=1);

// The second (and third) process of the sqlite race test: `php SqliteRaceHelper.php <dir>`.

use HopTop\Kit\Upgrade\Driver\Sqlite;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Migrator;
use HopTop\Kit\Upgrade\Registry;

require dirname(__DIR__, 3) . '/vendor/autoload.php';

$dir = $argv[1] ?? '';
$db = new PDO("sqlite:$dir/app.db", null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_TIMEOUT => 5]);
$reg = new Registry();
foreach (['1.1.0', '1.2.0'] as $v) {
    $reg->register(new Migration($v, 'appdb', static function () use ($db, $v): void {
        usleep(200_000); // a window for the other process
        $db->prepare('INSERT INTO applied (version, pid) VALUES (?, ?)')->execute([$v, getmypid()]);
    }));
}
$m = new Migrator('tool', '1.2.0', registry: $reg, dataDir: "$dir/data");
$m->addDriver(new Sqlite("$dir/app.db", name: 'appdb', dataDir: "$dir/data"));
$m->run();
echo "done\n";

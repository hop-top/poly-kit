<?php

declare(strict_types=1);

// Holds flock(LOCK_EX) on a file until stdin closes: `php FlockHolder.php <file>`. Prints "ready" once held.
// A second process standing in for another port's driver that locks the same path.

$fh = fopen($argv[1] ?? '', 'c');
if ($fh === false || !flock($fh, LOCK_EX)) {
    fwrite(STDERR, "cannot lock\n");
    exit(1);
}
echo "ready\n";
fflush(STDOUT);
stream_get_contents(STDIN);

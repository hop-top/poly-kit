# sqldb

Shared SQLite connection management (`modernc.org/sqlite`): `Open`,
`MustOpen`, `Migrate`.

Defaults applied to every pooled connection through DSN `_pragma`
parameters: WAL journal mode, `busy_timeout` (5000 ms), `foreign_keys=on`.

## Concurrent opens

Several goroutines or processes opening one SQLite file still in
rollback-journal mode race to switch it to WAL. SQLite returns
`SQLITE_BUSY` to the loser immediately (the busy handler is skipped on
that path to avoid deadlock), so `busy_timeout` alone does not help.

`Open` handles it: on a BUSY or LOCKED failure it closes the half-opened
pool and retries with backoff (10 ms doubling to 200 ms) until
`Options.BusyTimeout` (default 5000 ms) elapses; the last busy error is
then returned. Total wait can exceed the timeout by at most one backoff step (200 ms) plus one attempt.
Busy is detected by SQLite result code (primary code `SQLITE_BUSY` or
`SQLITE_LOCKED`, extended codes included) via the driver's typed error,
never by message text. Any other error returns at once, unretried.
Set `BusyTimeout` to bound the wait.

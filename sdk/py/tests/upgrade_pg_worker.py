"""One racing migrator process for test_upgrade_postgres_integration.py:
``python upgrade_pg_worker.py <flavor> <dsn> <table> <label> <hold-seconds>``.
Applies three migrations that each record who ran them and sleep, so a second
process started meanwhile has to wait on the lock."""

from __future__ import annotations

import sys
import tempfile
import time

from hop_top_kit.upgrade import Migration, Migrator, Registry
from hop_top_kit.upgrade.drivers import PostgresDriver


def main(flavor: str, dsn: str, table: str, label: str, hold: float) -> None:
    if flavor == "psycopg2":
        import psycopg2

        def connect():
            return psycopg2.connect(dsn)
    else:
        import psycopg

        def connect():
            return psycopg.connect(dsn)

    applied = f"{table}_applied"

    def step(version: str):
        def up() -> None:
            conn = connect()
            try:
                cur = conn.cursor()
                cur.execute(f"CREATE TABLE IF NOT EXISTS {applied} (version TEXT, label TEXT)")
                cur.execute(f"INSERT INTO {applied} VALUES (%s, %s)", (version, label))
                conn.commit()
            finally:
                conn.close()
            time.sleep(hold)

        return up

    reg = Registry()
    for v in ("1.1.0", "1.2.0", "1.3.0"):
        reg.register(Migration(version=v, schema="postgres", up=step(v)))
    m = Migrator("race", "1.3.0", registry=reg, data_dir=tempfile.mkdtemp())
    m.add_driver(PostgresDriver(connect, table))
    m.run()


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], float(sys.argv[5]))

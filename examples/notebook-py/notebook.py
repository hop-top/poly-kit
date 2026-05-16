"""Notebook CLI using kit-engine Python SDK."""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent.parent / "py" / "kit-engine"))

from kit_engine import KitEngine  # noqa: E402


def main():
    engine = KitEngine.start(app="notebook", no_peer=True, no_sync=True)
    notes = engine.collection("notes")

    cmd = sys.argv[1] if len(sys.argv) > 1 else "help"

    try:
        if cmd == "new":
            if len(sys.argv) < 3:
                die("usage: notebook.py new <title> [body]")
            data = {"title": sys.argv[2], "body": sys.argv[3] if len(sys.argv) > 3 else ""}
            doc = notes.create(data)
            print(f"Created note {doc['id']}")

        elif cmd == "list":
            docs = notes.list()
            if not docs:
                print("No notes found.")
            else:
                print(f"{'ID':<14}{'TITLE'}")
                for d in docs:
                    print(f"{d['id']:<14}{d.get('data', d).get('title', '')}")

        elif cmd == "get":
            if len(sys.argv) < 3:
                die("usage: notebook.py get <id>")
            doc = notes.get(sys.argv[2])
            data = doc.get("data", doc)
            print(f"ID:    {doc['id']}")
            print(f"Title: {data.get('title', '')}")
            print(f"Body:  {data.get('body', '')}")

        elif cmd == "edit":
            if len(sys.argv) < 4:
                die("usage: notebook.py edit <id> <title> [body]")
            data = {"title": sys.argv[3]}
            if len(sys.argv) > 4:
                data["body"] = sys.argv[4]
            notes.update(sys.argv[2], data)
            print(f"Updated note {sys.argv[2]}")

        elif cmd == "delete":
            if len(sys.argv) < 3:
                die("usage: notebook.py delete <id>")
            notes.delete(sys.argv[2])
            print(f"Deleted note {sys.argv[2]}")

        elif cmd == "history":
            if len(sys.argv) < 3:
                die("usage: notebook.py history <id>")
            versions = notes.history(sys.argv[2])
            if not versions:
                print("No versions found.")
            else:
                print(f"{'VERSION':<20}{'TIMESTAMP'}")
                for v in versions:
                    print(f"{v['id']:<20}{v.get('timestamp', '')}")

        elif cmd == "revert":
            if len(sys.argv) < 4:
                die("usage: notebook.py revert <id> <version>")
            notes.revert(sys.argv[2], sys.argv[3])
            print(f"Reverted note {sys.argv[2]} to version {sys.argv[3]}")

        else:
            print("Usage: notebook.py <new|list|get|edit|delete|history|revert> [args]")

    finally:
        engine.stop()


def die(msg: str):
    print(msg, file=sys.stderr)
    sys.exit(1)


if __name__ == "__main__":
    main()

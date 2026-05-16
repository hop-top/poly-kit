# notebook-py

Python notebook CLI using `kit-engine` Python SDK.

## What it demonstrates

- Starting kit serve via the Python SDK (`KitEngine.start()`)
- CRUD + versioning through the `Collection` API
- Graceful shutdown via `engine.stop()`

## Usage

```
python notebook.py new "My note" "Body text"
python notebook.py list
python notebook.py get <id>
python notebook.py edit <id> "New title" "New body"
python notebook.py delete <id>
python notebook.py history <id>
python notebook.py revert <id> <version>
```

Requires `kit` binary on PATH and `requests` package installed.

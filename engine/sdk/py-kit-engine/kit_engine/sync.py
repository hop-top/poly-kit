"""Sync operations client."""

from __future__ import annotations

import requests


class SyncClient:
    """REST client for kit sync endpoints."""

    def __init__(self, base_url: str, token: str | None = None):
        self._url = f"{base_url}/sync"
        self._token = token

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    def add_remote(
        self,
        name: str,
        url: str,
        mode: str = "both",
        filter: str = "",
    ) -> dict:
        resp = requests.post(
            f"{self._url}/remotes",
            json={"name": name, "url": url, "mode": mode, "filter": filter},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

    def remove_remote(self, name: str) -> None:
        resp = requests.delete(f"{self._url}/remotes/{name}", headers=self._headers())
        resp.raise_for_status()

    def status(self) -> dict:
        resp = requests.get(f"{self._url}/status")
        resp.raise_for_status()
        return resp.json()

    def push(self, diffs: list[dict]) -> dict:
        resp = requests.post(f"{self._url}/push", json=diffs, headers=self._headers())
        resp.raise_for_status()
        return resp.json()

    def pull(
        self,
        *,
        since_physical: int,
        since_logical: int,
        since_node: str,
    ) -> list[dict]:
        resp = requests.get(
            f"{self._url}/pull",
            params={
                "since_physical": since_physical,
                "since_logical": since_logical,
                "since_node": since_node,
            },
        )
        resp.raise_for_status()
        return resp.json()

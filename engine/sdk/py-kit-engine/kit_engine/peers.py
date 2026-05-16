"""Peer discovery client."""

from __future__ import annotations

import requests


class PeerClient:
    """REST client for kit peer endpoints."""

    def __init__(self, base_url: str, token: str | None = None):
        self._url = f"{base_url}/peers"
        self._token = token

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    def list(self) -> list[dict]:
        resp = requests.get(self._url)
        resp.raise_for_status()
        return resp.json()

    def trust(self, id: str) -> None:
        resp = requests.post(f"{self._url}/{id}/trust", headers=self._headers())
        resp.raise_for_status()

    def block(self, id: str) -> None:
        resp = requests.post(f"{self._url}/{id}/block", headers=self._headers())
        resp.raise_for_status()

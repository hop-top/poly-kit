"""CRUD operations on a typed collection."""

from __future__ import annotations

import requests


class Collection:
    """REST client for a single kit collection type."""

    def __init__(self, base_url: str, type_name: str, token: str | None = None):
        self._url = f"{base_url}/{type_name}"
        self._token = token

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    def create(self, data: dict) -> dict:
        resp = requests.post(f"{self._url}/", json=data, headers=self._headers())
        resp.raise_for_status()
        return resp.json()

    def get(self, id: str) -> dict:
        resp = requests.get(f"{self._url}/{id}")
        resp.raise_for_status()
        return resp.json()

    def list(
        self, *, limit: int = 20, offset: int = 0, sort: str = "", search: str = ""
    ) -> list[dict]:
        params: dict = {"limit": limit, "offset": offset}
        if sort:
            params["sort"] = sort
        if search:
            params["search"] = search
        resp = requests.get(f"{self._url}/", params=params)
        resp.raise_for_status()
        return resp.json()

    def update(self, id: str, data: dict) -> dict:
        resp = requests.put(f"{self._url}/{id}", json=data, headers=self._headers())
        resp.raise_for_status()
        return resp.json()

    def delete(self, id: str) -> None:
        resp = requests.delete(f"{self._url}/{id}", headers=self._headers())
        resp.raise_for_status()

    def history(self, id: str) -> list[dict]:
        resp = requests.get(f"{self._url}/{id}/history")
        resp.raise_for_status()
        return resp.json()["versions"]

    def history_topology(self, id: str) -> dict:
        resp = requests.get(f"{self._url}/{id}/history", params={"topology": "1"})
        resp.raise_for_status()
        return resp.json()

    def revert(self, id: str, version: int) -> dict:
        resp = requests.post(
            f"{self._url}/{id}/revert",
            json={"version": version},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

    def branches(self, id: str, *, live: bool = False) -> list[dict]:
        params = {"live": "1"} if live else None
        resp = requests.get(f"{self._url}/{id}/branches", params=params)
        resp.raise_for_status()
        return resp.json()["heads"]

    def fork(self, id: str, from_seq: int) -> dict:
        resp = requests.post(
            f"{self._url}/{id}/fork",
            json={"from_seq": from_seq},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

    def merge(self, id: str, source_seq: int, target_seq: int, data: dict) -> dict:
        resp = requests.post(
            f"{self._url}/{id}/merge",
            json={"source_seq": source_seq, "target_seq": target_seq, "data": data},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

    def prune(
        self,
        id: str,
        *,
        max_versions: int = 0,
        max_age_seconds: int = 0,
    ) -> dict:
        resp = requests.post(
            f"{self._url}/{id}/prune",
            json={"max_versions": max_versions, "max_age_seconds": max_age_seconds},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

    def abandon(self, id: str, seq: int) -> None:
        resp = requests.post(
            f"{self._url}/{id}/abandon",
            json={"seq": seq},
            headers=self._headers(),
        )
        resp.raise_for_status()

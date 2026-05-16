"""Identity management client."""

from __future__ import annotations

import requests


class IdentityClient:
    """REST client for kit identity endpoints."""

    def __init__(self, base_url: str, token: str | None = None):
        self._url = f"{base_url}/identity"
        self._token = token

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    def whoami(self) -> dict:
        resp = requests.get(self._url)
        resp.raise_for_status()
        return resp.json()

    def public_key(self) -> dict:
        return self.whoami()

    def verify(self, data: str, signature: str) -> dict:
        resp = requests.post(
            f"{self._url}/verify",
            json={"data": data, "signature": signature},
            headers=self._headers(),
        )
        resp.raise_for_status()
        return resp.json()

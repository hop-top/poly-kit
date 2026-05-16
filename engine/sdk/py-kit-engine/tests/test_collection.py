"""Tests for Collection CRUD operations."""

from __future__ import annotations

import responses
from kit_engine.collection import Collection

BASE = "http://localhost:9000"
TYPE = "notes"


@responses.activate
def test_create():
    responses.add(
        responses.POST,
        f"{BASE}/{TYPE}/",
        json={"id": "abc", "title": "hello"},
        status=201,
    )
    c = Collection(BASE, TYPE)
    result = c.create({"title": "hello"})
    assert result["id"] == "abc"


@responses.activate
def test_get():
    responses.add(
        responses.GET,
        f"{BASE}/{TYPE}/abc",
        json={"id": "abc", "title": "hello"},
        status=200,
    )
    c = Collection(BASE, TYPE)
    result = c.get("abc")
    assert result["title"] == "hello"


@responses.activate
def test_list_with_params():
    responses.add(
        responses.GET,
        f"{BASE}/{TYPE}/",
        json=[{"id": "1"}, {"id": "2"}],
        status=200,
    )
    c = Collection(BASE, TYPE)
    result = c.list(limit=10, search="foo")
    assert len(result) == 2
    assert "search=foo" in responses.calls[0].request.url


@responses.activate
def test_update():
    responses.add(
        responses.PUT,
        f"{BASE}/{TYPE}/abc",
        json={"id": "abc", "title": "updated"},
        status=200,
    )
    c = Collection(BASE, TYPE)
    result = c.update("abc", {"title": "updated"})
    assert result["title"] == "updated"


@responses.activate
def test_delete():
    responses.add(responses.DELETE, f"{BASE}/{TYPE}/abc", status=204)
    c = Collection(BASE, TYPE)
    c.delete("abc")


@responses.activate
def test_history():
    responses.add(
        responses.GET,
        f"{BASE}/{TYPE}/abc/history",
        json={"versions": [{"version": 2}, {"version": 1}]},
        status=200,
    )
    c = Collection(BASE, TYPE)
    result = c.history("abc")
    assert len(result) == 2


@responses.activate
def test_revert():
    responses.add(
        responses.POST,
        f"{BASE}/{TYPE}/abc/revert",
        json={"id": "abc", "data": {"title": "v1"}},
        status=200,
    )
    c = Collection(BASE, TYPE)
    result = c.revert("abc", 1)
    assert result["data"]["title"] == "v1"
    assert responses.calls[0].request.body == b'{"version": 1}'


@responses.activate
def test_mutations_send_auth_header():
    responses.add(
        responses.POST,
        f"{BASE}/{TYPE}/",
        json={"id": "abc"},
        status=201,
    )
    c = Collection(BASE, TYPE, token="tok")
    c.create({"title": "hello"})
    assert responses.calls[0].request.headers["Authorization"] == "Bearer tok"

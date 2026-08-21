"""Shared pytest configuration.

The MCP surface lives behind the optional ``mcp`` extra, so its test
modules cannot be collected in a base install. They are skipped when — and
only when — the protocol packages are genuinely absent: a missing *extra*
is a valid install shape, but a missing *fixture* or a broken import
inside an installed surface must still fail loudly, because a parity suite
that skips itself is worse than no parity suite at all.
"""

from __future__ import annotations

import importlib.util

import pytest

#: Test modules that need the ``mcp`` extra.
MCP_MODULES = (
    "test_mcp_conformance",
    "test_mcp_dispatch",
    "test_mcp_modern",
    "test_mcp_mrtr",
    "test_mcp_safety",
    "test_mcp_asgi",
)

MCP_EXTRA_INSTALLED = importlib.util.find_spec("mcp_types") is not None


def pytest_ignore_collect(collection_path, config):
    """Skip the MCP suites only when the extra is not installed."""
    if MCP_EXTRA_INSTALLED:
        return None
    if collection_path.stem in MCP_MODULES:
        return True
    return None


def pytest_report_header(config):
    """Say out loud whether the parity suite is running.

    Without this line a base-install run looks identical to a full run
    that happened to skip the contract, which is exactly the failure mode
    silent skipping creates.
    """
    if MCP_EXTRA_INSTALLED:
        return "mcp extra: installed (MCP surface suites active)"
    return "mcp extra: NOT installed (MCP surface suites skipped)"


@pytest.fixture(scope="session")
def mcp_extra_installed() -> bool:
    return MCP_EXTRA_INSTALLED

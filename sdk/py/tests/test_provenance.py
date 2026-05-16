"""Tests for hop_top_kit.provenance — data provenance (Factor 11)."""

import re
from datetime import UTC, datetime

from hop_top_kit.provenance import Provenance, with_provenance


class TestProvenance:
    def test_fields(self):
        p = Provenance(source="spaced-cli", timestamp="2024-01-15T10:30:00Z", method="list")
        assert p.source == "spaced-cli"
        assert p.timestamp == "2024-01-15T10:30:00Z"
        assert p.method == "list"


class TestWithProvenance:
    def test_wraps_data_with_meta(self):
        data = [{"name": "starlink-1"}]
        p = Provenance(source="spaced", timestamp="2024-01-15T10:30:00Z", method="list")
        result = with_provenance(data, p)

        assert result["data"] == data
        assert result["_meta"]["source"] == "spaced"
        assert result["_meta"]["timestamp"] == "2024-01-15T10:30:00Z"
        assert result["_meta"]["method"] == "list"

    def test_preserves_original_data(self):
        data = {"key": "value"}
        p = Provenance(source="s", timestamp="2024-01-01T00:00:00Z", method="get")
        result = with_provenance(data, p)
        assert result["data"] is data

    def test_meta_is_dict(self):
        p = Provenance(source="s", timestamp="2024-01-01T00:00:00Z", method="get")
        result = with_provenance(None, p)
        assert isinstance(result["_meta"], dict)
        assert set(result["_meta"].keys()) == {"source", "timestamp", "method"}


class TestTimestampFormat:
    def test_iso8601_format(self):
        ts = datetime.now(UTC).isoformat()
        p = Provenance(source="s", timestamp=ts, method="m")
        # ISO 8601 pattern
        assert re.match(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}", p.timestamp)

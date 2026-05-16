"""Tests for hop_top_kit.config — layered YAML config loader."""

import pytest
import yaml

from hop_top_kit.config import Options, load

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write(tmp_path, name: str, data: dict) -> str:
    """Write a YAML file and return its path as str."""
    p = tmp_path / name
    p.write_text(yaml.dump(data))
    return str(p)


# ---------------------------------------------------------------------------
# Merge order: system → user → project
# ---------------------------------------------------------------------------


def test_merges_system_user_project_in_order(tmp_path):
    """Later layers overwrite earlier layers."""
    sys = _write(tmp_path, "sys.yaml", {"a": 1, "b": "sys"})
    usr = _write(tmp_path, "usr.yaml", {"b": "usr", "c": 2})
    proj = _write(tmp_path, "proj.yaml", {"c": 99, "d": "proj"})

    dst = {}
    result = load(
        dst,
        Options(
            system_config_path=sys,
            user_config_path=usr,
            project_config_path=proj,
        ),
    )

    assert result["a"] == 1  # system only
    assert result["b"] == "usr"  # user overwrites system
    assert result["c"] == 99  # project overwrites user
    assert result["d"] == "proj"  # project only
    assert result is dst  # same reference, mutated in place


def test_later_layer_overwrites_earlier(tmp_path):
    """Explicit check: project value wins over system value for same key."""
    sys = _write(tmp_path, "sys.yaml", {"key": "from-system"})
    proj = _write(tmp_path, "proj.yaml", {"key": "from-project"})

    dst = {}
    load(dst, Options(system_config_path=sys, project_config_path=proj))
    assert dst["key"] == "from-project"


# ---------------------------------------------------------------------------
# Missing files
# ---------------------------------------------------------------------------


def test_missing_file_is_skipped(tmp_path):
    """A path that doesn't exist is silently skipped."""
    proj = _write(tmp_path, "proj.yaml", {"x": 42})
    dst = {}
    load(
        dst,
        Options(
            system_config_path="/nonexistent/does-not-exist.yaml",
            project_config_path=proj,
        ),
    )
    assert dst["x"] == 42


def test_all_missing_files_returns_empty(tmp_path):
    """All paths missing → dst unchanged."""
    dst = {"pre": "existing"}
    load(
        dst,
        Options(
            system_config_path="/no/a.yaml",
            user_config_path="/no/b.yaml",
            project_config_path="/no/c.yaml",
        ),
    )
    assert dst == {"pre": "existing"}


# ---------------------------------------------------------------------------
# Bad YAML raises
# ---------------------------------------------------------------------------


def test_bad_yaml_raises(tmp_path):
    """Malformed YAML must raise yaml.YAMLError (not be silently skipped)."""
    bad = tmp_path / "bad.yaml"
    bad.write_text("key: [unclosed")
    with pytest.raises(yaml.YAMLError):
        load({}, Options(system_config_path=str(bad)))


# ---------------------------------------------------------------------------
# env_override applied last
# ---------------------------------------------------------------------------


def test_env_override_applied_last(tmp_path):
    """env_override callback runs after all file layers."""
    proj = _write(tmp_path, "proj.yaml", {"mode": "file"})

    def override(d: dict) -> None:
        d["mode"] = "env"
        d["injected"] = True

    dst = {}
    load(dst, Options(project_config_path=proj, env_override=override))
    assert dst["mode"] == "env"
    assert dst["injected"] is True


def test_env_override_sees_merged_state(tmp_path):
    """env_override receives the fully merged dict."""
    sys = _write(tmp_path, "s.yaml", {"a": 1})
    usr = _write(tmp_path, "u.yaml", {"b": 2})

    seen: dict = {}

    def capture(d: dict) -> None:
        seen.update(d)

    load({}, Options(system_config_path=sys, user_config_path=usr, env_override=capture))
    assert seen == {"a": 1, "b": 2}


# ---------------------------------------------------------------------------
# Empty Options
# ---------------------------------------------------------------------------


def test_empty_options_returns_dst_unchanged():
    """No paths set, no override → dst returned unchanged."""
    dst = {"hello": "world"}
    result = load(dst, Options())
    assert result is dst
    assert result == {"hello": "world"}


def test_load_default_opts():
    """Calling load with only dst (default opts) works without error."""
    dst = {}
    result = load(dst)
    assert result is dst


# ---------------------------------------------------------------------------
# Return value / mutation
# ---------------------------------------------------------------------------


def test_returns_same_reference(tmp_path):
    """load() must return the same dict object it was given."""
    proj = _write(tmp_path, "p.yaml", {"z": 9})
    dst = {}
    assert load(dst, Options(project_config_path=proj)) is dst

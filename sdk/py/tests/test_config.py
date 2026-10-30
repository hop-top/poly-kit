"""Tests for hop_top_kit.config — layered YAML config loader."""

import copy
import json
from pathlib import Path

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


# ---------------------------------------------------------------------------
# Merge rules
# ---------------------------------------------------------------------------


def _load_layers(tmp_path, dst: dict, *bodies: str) -> dict:
    """Write each YAML body to a file and load them in slot order."""
    paths = []
    for i, body in enumerate(bodies):
        p = tmp_path / f"layer{i}.yaml"
        p.write_text(body, encoding="utf-8")
        paths.append(str(p))
    slots = dict(
        zip(("system_config_path", "user_config_path", "project_config_path"), paths, strict=False)
    )
    return load(dst, Options(**slots))


def test_nested_maps_merge_across_layers(tmp_path):
    got = _load_layers(tmp_path, {}, "db:\n  host: x\n  port: 1\n", "db:\n  port: 2\n")
    assert got == {"db": {"host": "x", "port": 2}}


def test_merges_at_every_depth_and_keeps_dst_defaults(tmp_path):
    dst = {"a": {"b": {"c": 1, "d": 2}, "keep": True}}
    _load_layers(tmp_path, dst, "a:\n  b:\n    d: 3\n    e: 4\n")
    assert dst == {"a": {"b": {"c": 1, "d": 3, "e": 4}, "keep": True}}


def test_does_not_mutate_nested_dst_dicts(tmp_path):
    defaults = {"c": 1}
    dst = {"b": defaults}
    _load_layers(tmp_path, dst, "b:\n  c: 2\n")
    assert dst == {"b": {"c": 2}}
    assert defaults == {"c": 1}


def test_empty_mapping_leaves_dict_as_is(tmp_path):
    dst = {"m": {"a": 1}}
    _load_layers(tmp_path, dst, "m: {}\n")
    assert dst == {"m": {"a": 1}}


def test_lists_replace_whole(tmp_path):
    dst = {"tags": ["d"]}
    _load_layers(tmp_path, dst, "tags: [a, b]\n", "tags: [c]\n")
    assert dst["tags"] == ["c"]
    _load_layers(tmp_path, dst, "tags: []\n")
    assert dst["tags"] == []


def test_null_leaves_scalar(tmp_path):
    dst = {"name": "d", "port": 1}
    _load_layers(tmp_path, dst, "name: a\n", "name: ~\nport: null\n")
    assert dst == {"name": "a", "port": 1}


def test_null_clears_dicts_and_lists(tmp_path):
    dst = {"m": {"a": 1}, "tags": ["x"], "deep": {"inner": {"k": "v"}}}
    _load_layers(tmp_path, dst, "m: ~\ntags: ~\ndeep:\n  inner: ~\n")
    assert dst == {"m": None, "tags": None, "deep": {"inner": None}}


def test_null_sets_absent_or_null_key(tmp_path):
    dst = {"n": None}
    _load_layers(tmp_path, dst, "n: ~\nfresh: ~\n")
    assert dst == {"n": None, "fresh": None}


def test_kind_conflict_later_value_replaces(tmp_path):
    dst = {
        "obj_to_scalar": {"a": 1},
        "scalar_to_obj": 5,
        "obj_to_list": {"a": 1},
        "list_to_obj": [1],
    }
    _load_layers(
        tmp_path,
        dst,
        "obj_to_scalar: 7\nscalar_to_obj: {b: 2}\nobj_to_list: [x]\nlist_to_obj: {c: 3}\n",
    )
    assert dst == {
        "obj_to_scalar": 7,
        "scalar_to_obj": {"b": 2},
        "obj_to_list": ["x"],
        "list_to_obj": {"c": 3},
    }


def test_aliased_mappings_do_not_leak_across_keys(tmp_path):
    dst: dict = {}
    _load_layers(tmp_path, dst, "a: &x {k: v}\nb: *x\n", "a:\n  k: changed\n")
    assert dst == {"a": {"k": "changed"}, "b": {"k": "v"}}


# ---------------------------------------------------------------------------
# Shared config-v1 load vectors (the struct-field rules)
# ---------------------------------------------------------------------------

# A dict has no field types, so a nested dict merges like a Go struct field.
# Vectors pinning typed-only rules (map entries replaced whole, pointers
# cleared by null, decode errors, unknown keys dropped) do not apply.
_LOAD_VECTORS = Path(__file__).resolve().parents[3] / "contracts" / "config-v1" / "load.json"
_STRUCT_RULE_CASES = (
    "later file wins per field",
    "struct fields merge",
    "nested struct fields merge",
    "lists replace",
    "map entries replace, others kept",
    "null leaves scalar",
    "start survives",
    "empty file is a no-op",
)
_ZERO = {
    "name": "",
    "port": 0,
    "ratio": 0,
    "debug": False,
    "tags": None,
    "inner": {"a": "", "b": 0, "deep": {"x": "", "y": ""}},
    "labels": None,
    "nest": None,
    "opt": None,
    "extra": None,
}


def _load_cases() -> dict:
    corpus = json.loads(_LOAD_VECTORS.read_text(encoding="utf-8"))
    assert corpus["version"] == "v1"
    return {c["name"]: c for c in corpus["cases"]}


@pytest.mark.parametrize("name", _STRUCT_RULE_CASES)
def test_config_v1_load_struct_rules(tmp_path, name):
    case = _load_cases()[name]
    assert case["ok"] and not case.get("overrides") and "generate" not in case
    dst = copy.deepcopy(case.get("start", _ZERO))
    got = _load_layers(tmp_path, dst, *case["files"])
    assert got == case["want"]

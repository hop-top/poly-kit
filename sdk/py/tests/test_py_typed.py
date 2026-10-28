"""The package ships the PEP 561 marker, so type checkers use its hints."""

from importlib.resources import files


def test_package_ships_py_typed_marker() -> None:
    assert (files("hop_top_kit") / "py.typed").is_file()

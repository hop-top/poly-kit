import click
from click.testing import CliRunner

from hop_top_kit.flagregister import (
    FlagDisplay,
    register_set_flag,
    register_text_flag,
)

runner = CliRunner()


def test_set_flag_prefix_only():
    @click.command()
    def cmd():
        pass

    sf = register_set_flag(cmd, "tag", "tags", FlagDisplay.PREFIX)
    result = runner.invoke(cmd, ["--tag", "feat", "--tag", "+docs"])
    assert result.exit_code == 0
    assert sf.values() == ["feat", "docs"]
    assert not any(p.name == "add_tag" for p in cmd.params)


def test_set_flag_verbose_only():
    @click.command()
    def cmd():
        pass

    sf = register_set_flag(cmd, "tag", "tags", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--add-tag", "feat", "--add-tag", "bug", "--remove-tag", "bug"])
    assert result.exit_code == 0
    assert sf.values() == ["feat"]
    assert not any(p.name == "tag" for p in cmd.params)


def test_set_flag_verbose_clear():
    @click.command()
    def cmd():
        pass

    sf = register_set_flag(cmd, "tag", "tags", FlagDisplay.VERBOSE)
    sf.set("existing")
    result = runner.invoke(cmd, ["--clear-tag"])
    assert result.exit_code == 0
    assert sf.values() == []


def test_set_flag_verbose_add_literal_plus():
    @click.command()
    def cmd():
        pass

    sf = register_set_flag(cmd, "tag", "tags", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--add-tag", "+ppl"])
    assert result.exit_code == 0
    assert sf.values() == ["+ppl"]


def test_set_flag_verbose_remove_literal_plus():
    @click.command()
    def cmd():
        pass

    sf = register_set_flag(cmd, "tag", "tags", FlagDisplay.VERBOSE)
    sf.add("+ppl")
    result = runner.invoke(cmd, ["--remove-tag", "+ppl"])
    assert result.exit_code == 0
    assert sf.values() == []


def test_text_flag_verbose_append_literal_plus():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--desc", "base", "--desc-append", "+1 improvement"])
    assert result.exit_code == 0
    assert tf.value() == "base\n+1 improvement"


def test_text_flag_verbose_prepend_literal_caret():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--desc", "base", "--desc-prepend", "^caret"])
    assert result.exit_code == 0
    assert tf.value() == "^caret\nbase"


def test_text_flag_prefix_only():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.PREFIX)
    result = runner.invoke(cmd, ["--desc", "base", "--desc", "+line2"])
    assert result.exit_code == 0
    assert tf.value() == "base\nline2"
    assert not any(p.name == "desc_append" for p in cmd.params)


def test_text_flag_verbose_append():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--desc", "base", "--desc-append", "added"])
    assert result.exit_code == 0
    assert tf.value() == "base\nadded"


def test_text_flag_verbose_append_inline():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--desc", "hello", "--desc-append-inline", " world"])
    assert result.exit_code == 0
    assert tf.value() == "hello world"


def test_text_flag_verbose_prepend():
    @click.command()
    def cmd():
        pass

    tf = register_text_flag(cmd, "desc", "description", FlagDisplay.VERBOSE)
    result = runner.invoke(cmd, ["--desc", "second", "--desc-prepend", "first"])
    assert result.exit_code == 0
    assert tf.value() == "first\nsecond"

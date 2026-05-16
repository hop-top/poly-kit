from hop_top_kit.setflag import SetFlag


def test_append_default():
    sf = SetFlag()
    sf.set("feat")
    sf.set("docs")
    assert sf.values() == ["feat", "docs"]


def test_append_explicit():
    sf = SetFlag()
    sf.set("+feat")
    sf.set("+docs")
    assert sf.values() == ["feat", "docs"]


def test_remove():
    sf = SetFlag(["feat", "bug", "docs"])
    sf.set("-bug")
    assert sf.values() == ["feat", "docs"]


def test_remove_nonexistent():
    sf = SetFlag(["feat"])
    sf.set("-nope")
    assert sf.values() == ["feat"]


def test_replace_all():
    sf = SetFlag(["old1", "old2"])
    sf.set("=new1,new2")
    assert sf.values() == ["new1", "new2"]


def test_clear_all():
    sf = SetFlag(["a", "b", "c"])
    sf.set("=")
    assert sf.values() == []


def test_str():
    sf = SetFlag(["a", "b"])
    assert str(sf) == "a,b"


def test_str_empty():
    sf = SetFlag()
    assert str(sf) == ""


def test_no_duplicates():
    sf = SetFlag()
    sf.set("feat")
    sf.set("feat")
    assert sf.values() == ["feat"]


def test_mixed_operations():
    sf = SetFlag()
    sf.set("a")
    sf.set("b")
    sf.set("c")
    sf.set("-b")
    sf.set("+d")
    assert sf.values() == ["a", "c", "d"]


def test_replace_after_append():
    sf = SetFlag()
    sf.set("a")
    sf.set("b")
    sf.set("=x")
    assert sf.values() == ["x"]


def test_comma_in_append():
    sf = SetFlag()
    sf.set("a,b")
    assert sf.values() == ["a", "b"]


def test_escape_literal_plus():
    sf = SetFlag()
    sf.set("=+ppl")
    assert sf.values() == ["+ppl"]


def test_escape_literal_minus():
    sf = SetFlag()
    sf.set("=-negative")
    assert sf.values() == ["-negative"]


def test_escape_literal_equals():
    sf = SetFlag()
    sf.set("==equals")
    assert sf.values() == ["=equals"]


def test_click_callback():
    sf = SetFlag()
    result = sf.click_callback(None, None, "feat")
    result = sf.click_callback(None, None, "+docs")
    result = sf.click_callback(None, None, "-feat")
    assert result == ["docs"]

from hop_top_kit.textflag import TextFlag


def test_replace_default():
    tf = TextFlag()
    tf.set("hello")
    assert tf.value() == "hello"
    tf.set("world")
    assert tf.value() == "world"


def test_replace_explicit():
    tf = TextFlag("old")
    tf.set("=new")
    assert tf.value() == "new"


def test_append_new_line():
    tf = TextFlag("first")
    tf.set("+second")
    assert tf.value() == "first\nsecond"


def test_append_multiple_lines():
    tf = TextFlag("line1")
    tf.set("+line2")
    tf.set("+line3")
    assert tf.value() == "line1\nline2\nline3"


def test_append_inline():
    tf = TextFlag("hello")
    tf.set("+= world")
    assert tf.value() == "hello world"


def test_prepend_new_line():
    tf = TextFlag("second")
    tf.set("^first")
    assert tf.value() == "first\nsecond"


def test_prepend_inline():
    tf = TextFlag("world")
    tf.set("^=hello ")
    assert tf.value() == "hello world"


def test_clear():
    tf = TextFlag("something")
    tf.set("=")
    assert tf.value() == ""


def test_append_to_empty():
    tf = TextFlag()
    tf.set("+line")
    assert tf.value() == "line"


def test_prepend_to_empty():
    tf = TextFlag()
    tf.set("^line")
    assert tf.value() == "line"


def test_str():
    tf = TextFlag("hello")
    assert str(tf) == "hello"


def test_escape_literal_plus():
    tf = TextFlag()
    tf.set("=+ppl")
    assert tf.value() == "+ppl"


def test_escape_literal_caret():
    tf = TextFlag()
    tf.set("=^weird")
    assert tf.value() == "^weird"


def test_escape_literal_equals():
    tf = TextFlag()
    tf.set("==equals")
    assert tf.value() == "=equals"


def test_mixed_operations():
    tf = TextFlag()
    tf.set("base")
    tf.set("+appended")
    tf.set("^prepended")
    assert tf.value() == "prepended\nbase\nappended"


def test_click_callback():
    tf = TextFlag()
    result = tf.click_callback(None, None, "base")
    result = tf.click_callback(None, None, "+added")
    assert result == "base\nadded"

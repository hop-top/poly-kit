package cli_test

import (
	"fmt"

	"hop.top/kit/go/console/cli"
)

// ExampleSetFlag shows that the operator is read from the first
// character of the whole argument and applies to the remainder. It is
// not a per-value prefix: only the leading operator is honored, and
// remove does not comma-split.
func ExampleSetFlag() {
	var sf cli.SetFlag
	_ = sf.Set("a,b,c") // no operator: append, comma-split
	fmt.Println(sf.Values())

	_ = sf.Set("+d,e") // leading +: append, comma-split
	fmt.Println(sf.Values())

	_ = sf.Set("+f,+g") // one operator; "+g" stays literal
	fmt.Println(sf.Values())

	_ = sf.Set("-b") // remove one member
	fmt.Println(sf.Values())

	_ = sf.Set("-a,c") // no comma-split on remove: nothing matches
	fmt.Println(sf.Values())

	_ = sf.Set("=x,y") // replace
	fmt.Println(sf.Values())

	_ = sf.Set("=") // clear
	fmt.Println(sf.Values())

	// Output:
	// [a b c]
	// [a b c d e]
	// [a b c d e f +g]
	// [a c d e f +g]
	// [a c d e f +g]
	// [x y]
	// []
}

// ExampleTextFlag shows the mutation operators and the order in which
// they are matched: "+=" and "^=" are tested before the bare "+" and
// "^", and "=" last.
func ExampleTextFlag() {
	var tf cli.TextFlag
	_ = tf.Set("body") // no operator: replace
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("+next") // newline append
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("+=more") // inline append
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("^head") // newline prepend
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("^=pre") // inline prepend
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("=reset") // explicit replace
	fmt.Printf("%q\n", tf.Value())

	_ = tf.Set("") // clear
	fmt.Printf("%q\n", tf.Value())

	// Output:
	// "body"
	// "body\nnext"
	// "body\nnextmore"
	// "head\nbody\nnextmore"
	// "prehead\nbody\nnextmore"
	// "reset"
	// ""
}

// Package secret scrubs credential values from adapter errors: a
// response body that echoes a key never surfaces it in an error
// message.
package secret

import (
	"fmt"
	"io"
	"strings"
)

// Of lists the non-blank credential values to scrub.
func Of(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// Scrub returns err with every secret replaced by REDACTED in its
// message; errors.Is and errors.As still reach err.
func Scrub(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	clean := msg
	for _, s := range secrets {
		clean = strings.ReplaceAll(clean, s, "REDACTED")
	}
	if clean == msg {
		return err
	}
	return &scrubbed{msg: clean, err: err}
}

type scrubbed struct {
	msg string
	err error
}

func (s *scrubbed) Error() string { return s.msg }
func (s *scrubbed) Unwrap() error { return s.err }

// Format keeps %+v and %#v to the scrubbed message.
func (s *scrubbed) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, s.msg) }

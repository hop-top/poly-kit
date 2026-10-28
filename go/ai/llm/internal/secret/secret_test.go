package secret

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOfSkipsBlank(t *testing.T) {
	assert.Equal(t, []string{"k", "t"}, Of("k", "", "  ", "t"))
	assert.Nil(t, Of("", " "))
}

func TestScrub(t *testing.T) {
	base := errors.New("bad key k1 and t2")
	got := Scrub(base, Of("k1", "t2"))
	assert.Equal(t, "bad key REDACTED and REDACTED", got.Error())
	assert.Equal(t, got.Error(), fmt.Sprintf("%+v", got))
	assert.ErrorIs(t, got, base)
	assert.Same(t, base, Scrub(base, Of("zz")))
	assert.NoError(t, Scrub(nil, Of("k1")))
}

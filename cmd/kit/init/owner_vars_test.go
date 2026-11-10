package kitinit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOwnerSlug(t *testing.T) {
	cases := []struct {
		name, org, module, author, project, want string
	}{
		{"org wins", "Hop-Top", "github.com/x/y", "Ada L", "demo", "hop-top"},
		{"module owner segment", "", "github.com/test-user/demo", "Ada L", "demo", "test-user"},
		{"author without module owner", "", "hop.top", "Ada Lovelace", "demo", "ada-lovelace"},
		{"name as last resort", "", "", "", "my-app", "my-app"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ownerSlug(c.org, c.module, c.author, c.project))
		})
	}
}

func TestToPascal(t *testing.T) {
	for in, want := range map[string]string{
		"demo":         "Demo",
		"my-app":       "MyApp",
		"hop_top":      "HopTop",
		"ada lovelace": "AdaLovelace",
		"":             "",
	} {
		assert.Equal(t, want, toPascal(in), in)
	}
}

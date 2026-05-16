package avatar

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

// DicebearProviderName is the registered name of the built-in dicebear provider.
const DicebearProviderName = "dicebear"

const (
	// DicebearDefaultStyle is used when Options.Style is empty for
	// the dicebear provider. "shapes" produces abstract identicons
	// with no human/figurative content.
	DicebearDefaultStyle = "shapes"

	// DicebearDefaultFormat is used when Options.Format is empty.
	DicebearDefaultFormat = "svg"

	// DicebearAPIVersion is the dicebear HTTP API version targeted.
	DicebearAPIVersion = "9.x"

	// DicebearBaseURL is the host segment of the generated URLs.
	DicebearBaseURL = "https://api.dicebear.com"
)

// DicebearStyles is the published list of dicebear styles as of the
// 9.x API. Used for Provider.Styles() introspection; not validated
// against — passing an unknown style yields a URL that the dicebear
// endpoint may reject. Callers that want strict validation can check
// against this list themselves.
var DicebearStyles = []string{
	"adventurer",
	"adventurer-neutral",
	"avataaars",
	"avataaars-neutral",
	"big-ears",
	"big-ears-neutral",
	"big-smile",
	"bottts",
	"bottts-neutral",
	"croodles",
	"croodles-neutral",
	"dylan",
	"fun-emoji",
	"glass",
	"icons",
	"identicon",
	"initials",
	"lorelei",
	"lorelei-neutral",
	"micah",
	"miniavs",
	"notionists",
	"notionists-neutral",
	"open-peeps",
	"personas",
	"pixel-art",
	"pixel-art-neutral",
	"rings",
	"shapes",
	"thumbs",
}

// dicebearProvider implements Provider via the dicebear.com HTTP API.
// It is registered automatically as the package default.
type dicebearProvider struct{}

func (dicebearProvider) Name() string { return DicebearProviderName }

func (dicebearProvider) Styles() []string {
	out := make([]string, len(DicebearStyles))
	copy(out, DicebearStyles)
	sort.Strings(out)
	return out
}

func (dicebearProvider) Generate(_ context.Context, opts Options) (string, error) {
	if opts.Seed == "" {
		return "", fmt.Errorf("dicebear: Seed is required")
	}
	style := opts.Style
	if style == "" {
		style = DicebearDefaultStyle
	}
	format := opts.Format
	if format == "" {
		format = DicebearDefaultFormat
	}

	q := url.Values{}
	q.Set("seed", opts.Seed)
	if opts.Size > 0 {
		q.Set("size", strconv.Itoa(opts.Size))
	}
	for k, v := range opts.Extra {
		q.Set(k, v)
	}

	return fmt.Sprintf("%s/%s/%s/%s?%s",
		DicebearBaseURL,
		DicebearAPIVersion,
		url.PathEscape(style),
		url.PathEscape(format),
		q.Encode(),
	), nil
}

func init() {
	RegisterProvider(dicebearProvider{})
}

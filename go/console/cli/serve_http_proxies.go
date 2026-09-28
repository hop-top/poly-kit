package cli

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/spf13/cast"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// trustedProxiesBlock is an HTTP listener's trusted-proxy list:
// services.<svc>.trusted_proxies, with a shared default under
// services.all.trusted_proxies. It is a list of CIDRs and addresses,
// registered in svcconfig as a value block; empty trusts no proxy.
const trustedProxiesBlock = "trusted_proxies"

// trustedProxies resolves the listener's trusted-proxy networks. The
// service's list replaces the shared one; a string is one entry or
// several, comma or space separated, the way an environment variable
// carries a list. An entry that is not a CIDR or an address is an
// error naming the key.
func (p httpPlane) trustedProxies() ([]netip.Prefix, error) {
	raw, key, ok := svcconfig.New(p.viper()).Lookup(p.l.Service, trustedProxiesBlock, "")
	if !ok {
		return nil, nil
	}
	var entries []string
	for _, s := range cast.ToStringSlice(raw) {
		entries = append(entries, strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })...)
	}
	prefixes, err := api.ParseTrustedProxies(entries)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return prefixes, nil
}

// validateTrustedProxies refuses a trusted_proxies value of the wrong
// shape or an entry that does not parse, before anything binds.
func (p httpPlane) validateTrustedProxies() error {
	if v := p.viper(); v != nil {
		err := svcconfig.New(v).ValidateBlock(trustedProxiesBlock, p.l.Service, svcconfig.Shared)
		if err != nil {
			return err
		}
	}
	_, err := p.trustedProxies()
	return err
}

// clientAddress is HTTP-plane slot 2: the client behind a trusted
// proxy becomes the request's RemoteAddr, so the access log, the
// audit record and the rate limiter below all read the one address.
func (p httpPlane) clientAddress() (api.Middleware, error) {
	prefixes, err := p.trustedProxies()
	if err != nil {
		return nil, err
	}
	return api.ClientAddress(api.ClientAddressConfig{TrustedProxies: prefixes}), nil
}

package kv

import (
	"fmt"
	"log/slog"
	"strings"
)

// redacted replaces a credential in every rendering of a Config.
const redacted = "[REDACTED]"

// configView is what a Config prints as: the same fields, with every
// credential redacted and TLS reduced to whether it is set. It is a
// distinct type so that formatting it does not recurse into String.
type configView struct {
	Backend   string
	Path      string
	Endpoints []string
	Prefix    string
	Username  string
	Password  string
	TLS       bool
	DSN       string
	Table     string
}

func (c Config) view() configView {
	v := configView{
		Backend:  c.Backend,
		Path:     c.Path,
		Prefix:   c.Prefix,
		Username: c.Username,
		TLS:      c.TLS != nil,
		DSN:      redactDSN(c.DSN),
		Table:    c.Table,
	}
	if c.Password != "" {
		v.Password = redacted
	}
	if c.Endpoints != nil {
		v.Endpoints = make([]string, len(c.Endpoints))
		for i, ep := range c.Endpoints {
			v.Endpoints[i] = redactEndpoint(ep)
		}
	}
	return v
}

// String renders c with its credentials redacted: Password, the password
// in DSN and any userinfo in Endpoints become "[REDACTED]". %v, %+v and %s
// all print this, so a Config is safe in a log line or a wrapped error.
func (c Config) String() string {
	return fmt.Sprintf("%+v", c.view())
}

// GoString makes %#v print the redacted form too.
func (c Config) GoString() string {
	return "kv.Config" + c.String()
}

// LogValue makes log/slog print the redacted form under every handler;
// the JSON handler would otherwise marshal the exported fields as-is.
func (c Config) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

// redactEndpoint redacts the userinfo of a host-based endpoint, whatever
// its scheme. A unix(s) socket path has no authority, so an "@" in it is
// part of the name and is left alone.
func redactEndpoint(ep string) string {
	if strings.HasPrefix(ep, "unix:") || strings.HasPrefix(ep, "unixs:") {
		return ep
	}
	prefix, rest := "", ep
	if i := strings.Index(ep, "://"); i >= 0 {
		prefix, rest = ep[:i+3], ep[i+3:]
		authority := rest
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			authority = rest[:j]
		}
		at := strings.LastIndex(authority, "@")
		if at < 0 {
			return ep
		}
		return prefix + redacted + rest[at:]
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return ep
	}
	return redacted + rest[at:]
}

// redactDSN redacts the password of a go-sql-driver/mysql DSN,
// [user[:password]@][net[(addr)]]/dbname[?params], keeping the user. It
// splits as the driver's ParseDSN does: the user part ends at the last
// "@" before the last "/".
func redactDSN(dsn string) string {
	end := strings.LastIndex(dsn, "/")
	if end < 0 {
		end = len(dsn)
	}
	at := strings.LastIndex(dsn[:end], "@")
	if at < 0 {
		return dsn
	}
	user, _, hasPassword := strings.Cut(dsn[:at], ":")
	if !hasPassword {
		return dsn
	}
	return user + ":" + redacted + dsn[at:]
}

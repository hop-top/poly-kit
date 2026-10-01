package llm

import (
	"net/url"
	"strconv"
	"strings"
)

// redacted replaces each credential [RedactURI] masks.
const redacted = "REDACTED"

// RedactURI returns raw with its credentials replaced by REDACTED, for
// error messages and logs. Scheme, host, model and every other param
// are kept, so the result still says which provider and model failed.
//
// It masks the value of each param whose name marks a credential:
// key, token, secret, password, auth, authorization, signature, sig,
// credential(s), and names ending in _key, _token, _secret, _password,
// _signature, _credential(s) or apikey (api_key, x-api-key,
// access_token, X-Amz-Signature), compared case-insensitively with "-"
// read as "_". Kit itself reads only api_key; the rest cover a base_url
// or media URL a caller built. It also masks URL userinfo, in raw or in
// a param value such as base_url.
//
// raw need not parse. RedactURI works on the text, splitting params on
// ? and & as [ParseURI] does, so a malformed URI ("openai/gpt-4?api_key=…")
// is masked too. In a model URI, "name@version" ("vertex://claude@2024")
// is a model id, not userinfo, and is kept; userinfo holding a ":" or
// followed by a path ("openai://user:pass@proxy/gpt-4") is masked, as
// is any userinfo in an http(s) URL or a param value.
func RedactURI(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for {
		i := strings.IndexAny(raw, "?&")
		if i < 0 {
			b.WriteString(redactSegment(raw))
			return b.String()
		}
		b.WriteString(redactSegment(raw[:i]))
		b.WriteByte(raw[i])
		raw = raw[i+1:]
	}
}

// redactSegment masks one ?/&-delimited piece of a URI: a credential
// param's value, or userinfo in the piece (or in a param's value).
func redactSegment(seg string) string {
	k, v, ok := strings.Cut(seg, "=")
	if !ok || strings.Contains(k, "://") {
		return redactUserinfo(seg, false)
	}
	if credentialParam(k) {
		if v == "" {
			return seg
		}
		return k + "=" + redacted
	}
	return k + "=" + redactUserinfo(v, true)
}

// redactUserinfo masks the userinfo of s's authority: the text after
// "://" (or from the start when s has none) up to the first "/". strict
// masks any userinfo; otherwise only userinfo that cannot be part of a
// model id, as [RedactURI] describes.
func redactUserinfo(s string, strict bool) string {
	start, scheme := 0, ""
	if i := strings.Index(s, "://"); i >= 0 {
		start, scheme = i+3, strings.ToLower(s[:i])
	}
	rest := s[start:]
	end := strings.IndexByte(rest, '/')
	hasPath := end >= 0
	if !hasPath {
		end = len(rest)
	}
	at := strings.LastIndexByte(rest[:end], '@')
	if at <= 0 {
		return s
	}
	credential := strict || scheme == "http" || scheme == "https" ||
		(hasPath && start > 0) || strings.Contains(rest[:at], ":")
	if !credential {
		return s // a "name@version" model id
	}
	return s[:start] + redacted + rest[at:]
}

// credentialParam reports whether a param named name carries a
// credential, per [RedactURI].
func credentialParam(name string) bool {
	if u, err := url.QueryUnescape(name); err == nil {
		name = u
	}
	name = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
	switch name {
	case "key", "apikey", "token", "secret", "password", "passwd", "pwd",
		"auth", "authorization", "signature", "sig", "credential", "credentials":
		return true
	}
	for _, suffix := range []string{
		"_key", "apikey", "_token", "_secret", "_password",
		"_signature", "_credential", "_credentials",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// RedactURLError masks, with [RedactURI], the URL of every [*url.Error]
// in err's chain. net/http quotes the request URL in each transport
// error and masks only a userinfo password; a base URL's query, or a
// token held as the userinfo name, would otherwise surface.
//
// The *url.Error values are rewritten in place, so pass only an error
// the caller owns, such as one an http.Client call just returned, and
// use the result in place of err from then on. The result is err
// itself, unless a wrapper above a *url.Error had already formatted
// its message (fmt.Errorf does so eagerly): then it is an error
// carrying the masked message that unwraps to err, so errors.Is and
// errors.As still reach every error in the chain.
func RedactURLError(err error) error {
	if err == nil {
		return nil
	}
	var masked []urlPair
	redactURLErrors(err, &masked)
	if len(masked) == 0 {
		return err
	}
	after := err.Error()
	msg := after
	for _, p := range masked {
		msg = strings.ReplaceAll(msg, p.from, p.to)
		// %q escapes some bytes the raw URL holds (\x7f, quotes).
		msg = strings.ReplaceAll(msg, quoteInner(p.from), quoteInner(p.to))
	}
	if msg == after {
		return err // every message reads the masked URL already
	}
	return &redactedError{msg: msg, err: err}
}

// urlPair is a *url.Error URL before and after [RedactURI].
type urlPair struct{ from, to string }

// quoteInner is s as %q renders it, without the surrounding quotes.
func quoteInner(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}

func redactURLErrors(err error, masked *[]urlPair) {
	for err != nil {
		if ue, ok := err.(*url.Error); ok {
			if to := RedactURI(ue.URL); to != ue.URL {
				*masked = append(*masked, urlPair{from: ue.URL, to: to})
				ue.URL = to
			}
		}
		switch u := err.(type) {
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				redactURLErrors(e, masked)
			}
			return
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		default:
			return
		}
	}
}

// redactedError replaces the message of an error whose wrapper had
// already formatted a *url.Error's URL into it.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

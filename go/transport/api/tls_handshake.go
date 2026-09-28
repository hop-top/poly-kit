package api

import (
	"bytes"
	"log"
	"strings"
)

// CodeTLSHandshake is the refusal code a TLS listener counts a failed
// handshake under: a client that sent no TLS, one whose certificate
// the CA bundle did not verify or that is revoked, one that offered
// nothing the server accepts, one that hung up mid-handshake. It is
// an observability code only — no request exists, so nothing is
// answered with it.
const CodeTLSHandshake = "tls_handshake"

// handshakeErrorPrefix is how net/http's server reports a failed TLS
// handshake on its ErrorLog: "http: TLS handshake error from <remote
// address>: <reason>".
const handshakeErrorPrefix = "http: TLS handshake error from "

// HandshakeErrorLog returns an [http.Server] ErrorLog that hands each
// failed TLS handshake the server reports to onFailure — the client's
// address and the reason — and passes every other line to next, the
// server's previous ErrorLog (the standard logger when nil), unchanged.
//
// It is how a listener takes handshake failures out of the standard
// logger, where a scanner or a misconfigured client would otherwise
// write an unleveled line per connection, and into its own logging and
// metrics. onFailure runs on the connection's goroutine and must not
// block.
func HandshakeErrorLog(next *log.Logger, onFailure func(remoteAddr, reason string)) *log.Logger {
	if next == nil {
		next = log.Default()
	}
	return log.New(handshakeWriter{next: next, onFailure: onFailure}, "", 0)
}

type handshakeWriter struct {
	next      *log.Logger
	onFailure func(remoteAddr, reason string)
}

func (w handshakeWriter) Write(p []byte) (int, error) {
	line := string(bytes.TrimSuffix(p, []byte("\n")))
	if rest, ok := strings.CutPrefix(line, handshakeErrorPrefix); ok && w.onFailure != nil {
		// A remote address holds colons (an IPv6 one, the port) but
		// never ": ", which separates it from the reason.
		addr, reason, _ := strings.Cut(rest, ": ")
		w.onFailure(addr, reason)
		return len(p), nil
	}
	w.next.Print(line)
	return len(p), nil
}

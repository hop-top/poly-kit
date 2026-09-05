package netpolicy_test

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"

	"hop.top/kit/go/core/netpolicy"
)

// Route a raw net.Dialer through the policy. net/smtp is the shape:
// dial first, hand the conn to the protocol client.
func ExampleGuardDial_smtp() {
	dial := netpolicy.GuardDial((&net.Dialer{}).DialContext)

	ctx := netpolicy.WithOffline(context.Background(), true)
	conn, err := dial(ctx, "tcp", "mail.example.com:25")
	if err != nil {
		fmt.Println("refused:", errors.Is(err, netpolicy.ErrOffline))
		return
	}
	defer conn.Close()
	c, _ := smtp.NewClient(conn, "mail.example.com")
	defer c.Close()
	// Output: refused: true
}

// Raw TLS: dial through the policy, then layer tls.Client over the conn
// rather than calling tls.Dial, which opens its own socket.
func ExampleGuardDial_tls() {
	dial := netpolicy.GuardDial(nil) // nil base -> zero net.Dialer

	ctx := netpolicy.WithOffline(context.Background(), true)
	conn, err := dial(ctx, "tcp", "api.example.com:443")
	if err != nil {
		fmt.Println("refused:", errors.Is(err, netpolicy.ErrOffline))
		return
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: "api.example.com"})
	defer tlsConn.Close()
	// Output: refused: true
}

// database/sql: go-sql-driver/mysql takes a dial function on its Config,
// and passes the query context down to it, so the marker survives.
//
//	cfg := mysql.NewConfig()
//	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
//	        return netpolicy.GuardDial(nil)(ctx, network, addr)
//	}
//	db := sql.OpenDB(must(mysql.NewConnectorConfig(cfg)))
//
// sql.Open is lazy: nothing dials until the first query, so the refusal
// surfaces on Ping/Query rather than on Open. Drivers that expose no
// dialer hook cannot be covered — consult IsOffline before opening.
func ExampleCheckDial_databaseSQL() {
	ctx := netpolicy.WithOffline(context.Background(), true)

	// Without a dialer hook, gate the open on the same decision.
	if err := netpolicy.CheckDial(ctx, "tcp", "db.example.com:3306"); err != nil {
		fmt.Println("refused:", errors.Is(err, netpolicy.ErrOffline))
		return
	}
	_, _ = sql.Open("mysql", "user@tcp(db.example.com:3306)/kit")
	// Output: refused: true
}

// A library that dials its own socket but accepts an *http.Client —
// coder/websocket is the case in kit — is covered by handing it a client
// whose transport is guarded.
func ExampleGuard_webSocket() {
	hc := &http.Client{Transport: netpolicy.Guard(http.DefaultTransport)}

	// websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: hc})
	_ = hc
	fmt.Println("ok")
	// Output: ok
}

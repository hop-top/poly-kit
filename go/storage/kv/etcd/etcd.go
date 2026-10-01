package etcd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv"
)

// Store implements kv.Store backed by etcd.
type Store struct {
	client *clientv3.Client
	prefix string
	// cancel ends the client's lifetime context (see connect).
	cancel context.CancelFunc
}

var _ kv.Store = (*Store)(nil)

// New connects to an etcd cluster and returns a prefixed Store.
//
// It is NewContext with a background context, kept for callers that have
// none to offer. Because the offline marker travels on a context, a Store
// built this way is opened without consulting the policy. Prefer NewContext.
func New(endpoints []string, prefix string, opts ...Option) (*Store, error) {
	return NewContext(context.Background(), endpoints, prefix, opts...)
}

// NewContext connects to an etcd cluster and returns a prefixed Store,
// honoring the network policy carried by ctx. Credentials and transport
// security are passed as options (WithAuth, WithTLS), never in an
// endpoint: the client does not read URL userinfo, and an endpoint that
// carries any is rejected.
//
// Unlike the tidb driver, etcd cannot be brought under the policy by its
// dial hook alone. grpc.WithContextDialer is invoked by gRPC's own
// connection manager on a background context, not on the context of the
// call that triggered the dial, so the offline marker never reaches
// netpolicy.CheckDial down that path. Without credentials clientv3.New is
// also non-blocking: it returns before any connection is attempted, so
// there is no open-time dial to intercept even in principle.
//
// The endpoints are therefore checked here, against the policy on ctx,
// before the client is constructed — netpolicy.CheckDial is the seam for
// exactly this case, a hook that is not shaped like a dialer. The guarded
// dialer is installed as well, so a dial that does carry the marker is
// refused beneath us too, but the check above is what makes the refusal
// reliable.
//
// With credentials the client authenticates before it returns, so the
// open blocks until the cluster answers; ctx bounds that wait. A ctx that
// ends before the client is built fails the open with ctx's error.
func NewContext(ctx context.Context, endpoints []string, prefix string, opts ...Option) (*Store, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	for _, ep := range endpoints {
		if err := checkEndpoint(ep); err != nil {
			return nil, err
		}
	}
	if err := checkTransport(endpoints, o.tls != nil); err != nil {
		return nil, err
	}
	for _, ep := range endpoints {
		network, addr := dialTarget(ep)
		if err := netpolicy.CheckDial(ctx, network, addr); err != nil {
			return nil, fmt.Errorf("etcd kv: connect: %w", err)
		}
	}
	client, cancel, err := connect(ctx, clientConfig(endpoints, o))
	if err != nil {
		return nil, err
	}
	return &Store{client: client, prefix: prefix, cancel: cancel}, nil
}

// clientConfig builds the client configuration: endpoints as given,
// credentials and TLS from o, and the policy-guarded dialer.
func clientConfig(endpoints []string, o options) clientv3.Config {
	guarded := netpolicy.GuardDial(nil)
	return clientv3.Config{
		Endpoints: endpoints,
		Username:  o.username,
		Password:  o.password,
		TLS:       o.tls,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(dctx context.Context, addr string) (net.Conn, error) {
				network := "tcp"
				if target, ok := strings.CutPrefix(addr, "unix:"); ok {
					network, addr = "unix", target
				}
				return guarded(dctx, network, addr)
			}),
		},
	}
}

// connect builds the client with construction bounded by ctx.
//
// clientv3.New authenticates before returning when credentials are set: a
// blocking RPC on the client's own context (Config.Context), which by
// default never ends, so an unreachable cluster would hang the open. That
// context must outlive the open, though — it is the client's lifetime — so
// it is not ctx itself but a fresh one that ctx cancels only while the
// client is being built. The returned cancel ends it; Store.Close calls it.
func connect(ctx context.Context, cfg clientv3.Config) (*clientv3.Client, context.CancelFunc, error) {
	life, cancel := context.WithCancel(context.Background())
	cfg.Context = life
	stop := context.AfterFunc(ctx, cancel)
	client, err := clientv3.New(cfg)
	if !stop() {
		// ctx ended while the client was being built, and has already
		// canceled its lifetime: whatever New returned is unusable.
		if client != nil {
			_ = client.Close()
		}
		cancel()
		return nil, nil, fmt.Errorf("etcd kv: connect: %w", ctx.Err())
	}
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("etcd kv: connect: %w", err)
	}
	return client, cancel, nil
}

// checkEndpoint rejects an endpoint whose dial target could leak or
// could never connect, before anything prints or dials it.
//
// The client (clientv3 internal/endpoint, not importable) interprets
// exactly three forms: bare host:port, http(s):// — reduced to the URL
// host, scheme case-folded by net/url — and the unix(s) socket forms.
// Anything else carrying "://" is handed to the dialer verbatim as a TCP
// address: it can never connect, and the client's own logger prints it,
// userinfo included. Such an endpoint is rejected; the error names the
// scheme only when it is a well-formed one, so nothing else is echoed.
//
// Userinfo is rejected in every host-based form (see checkUserinfo):
// the client never sends it. A socket path is a file or abstract name,
// not an authority: an "@" there is not userinfo, and unix dials are
// never refused by the policy, so the socket forms pass unchanged.
func checkEndpoint(ep string) error {
	if strings.HasPrefix(ep, "unix:") || strings.HasPrefix(ep, "unixs:") {
		return nil
	}
	scheme, rest, hasScheme := strings.Cut(ep, "://")
	if !hasScheme {
		return checkUserinfo(ep)
	}
	if isHTTPScheme(scheme) {
		// The authority ends at the first path, query or fragment
		// delimiter; an "@" past it is not userinfo.
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		return checkUserinfo(rest)
	}
	if !validScheme(scheme) {
		return errors.New("etcd kv: endpoint has a malformed scheme; use http://, https://, unix://, unixs:// or host:port")
	}
	return fmt.Errorf("etcd kv: endpoint scheme %q not supported; use http://, https://, unix://, unixs:// or host:port", scheme)
}

// checkUserinfo rejects an authority that carries userinfo. The client
// never sends it — it dials url.Host for http(s), the raw string
// otherwise — so credentials there would be silently dropped, or printed
// in a refusal. The error names only the part after the last "@" and
// points at the supported credential fields.
func checkUserinfo(authority string) error {
	i := strings.LastIndex(authority, "@")
	if i < 0 {
		return nil
	}
	return fmt.Errorf("etcd kv: endpoint %q: credentials in an endpoint are not supported; "+
		"set kv.Config Username and Password, or pass etcd.WithAuth", authority[i+1:])
}

// isHTTPScheme reports whether scheme is one the client reduces to its URL
// host. net/url folds a scheme to lower case, so the match does too.
func isHTTPScheme(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

// validScheme reports whether s is an RFC 3986 scheme:
// ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ).
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// dialTarget reduces an etcd endpoint to the network and address a dial
// would use, mirroring the client's own endpoint interpretation
// (clientv3 internal/endpoint, which is not importable).
//
// etcd accepts http(s):// and unix(s):// schemes as well as bare host:port.
// url.Parse cannot be used alone: it rejects "127.0.0.1:2379" outright and
// reads "localhost:2379" as scheme "localhost". checkEndpoint has already
// rejected every other scheme; anything left unrecognized is passed
// through as a TCP address, so it is still policy-checked rather than
// silently exempted.
func dialTarget(ep string) (network, addr string) {
	for _, scheme := range []string{"unix://", "unixs://"} {
		if rest, ok := strings.CutPrefix(ep, scheme); ok {
			return "unix", rest
		}
	}
	for _, scheme := range []string{"unix:", "unixs:"} {
		if rest, ok := strings.CutPrefix(ep, scheme); ok {
			return "unix", rest
		}
	}
	if scheme, rest, ok := strings.Cut(ep, "://"); ok && isHTTPScheme(scheme) {
		// Reduce to host:port, as url.Host would: the authority
		// ends at the first path, query or fragment delimiter, and
		// userinfo ends at its last "@". The client dials url.Host,
		// so credentials are never part of the target; keeping them
		// would print them in a refusal and hide a loopback host.
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest = rest[i+1:]
		}
		return "tcp", rest
	}
	return "tcp", ep
}

func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.client.Put(ctx, s.prefix+key, string(value))
	return err
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.client.Get(ctx, s.prefix+key)
	if err != nil {
		return nil, false, err
	}
	if len(resp.Kvs) == 0 {
		return nil, false, nil
	}
	return resp.Kvs[0].Value, true, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.Delete(ctx, s.prefix+key)
	return err
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	full := s.prefix + prefix
	resp, err := s.client.Get(ctx, full, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Strip the store prefix, return relative key.
		keys = append(keys, string(kv.Key)[len(s.prefix):])
	}
	return keys, nil
}

func (s *Store) Close() error {
	err := s.client.Close()
	s.cancel()
	return err
}

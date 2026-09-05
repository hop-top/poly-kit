package etcd

import (
	"context"
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
}

var _ kv.Store = (*Store)(nil)

// New connects to an etcd cluster and returns a prefixed Store.
//
// It is NewContext with a background context, kept for callers that have
// none to offer. Because the offline marker travels on a context, a Store
// built this way is opened without consulting the policy. Prefer NewContext.
func New(endpoints []string, prefix string) (*Store, error) {
	return NewContext(context.Background(), endpoints, prefix)
}

// NewContext connects to an etcd cluster and returns a prefixed Store,
// honoring the network policy carried by ctx.
//
// Unlike the tidb driver, etcd cannot be brought under the policy by its
// dial hook alone. grpc.WithContextDialer is invoked by gRPC's own
// connection manager on a background context, not on the context of the
// call that triggered the dial, so the offline marker never reaches
// netpolicy.CheckDial down that path. clientv3.New is also non-blocking:
// it returns before any connection is attempted, so there is no open-time
// dial to intercept even in principle.
//
// The endpoints are therefore checked here, against the policy on ctx,
// before the client is constructed — netpolicy.CheckDial is the seam for
// exactly this case, a hook that is not shaped like a dialer. The guarded
// dialer is installed as well, so a dial that does carry the marker is
// refused beneath us too, but the check above is what makes the refusal
// reliable.
func NewContext(ctx context.Context, endpoints []string, prefix string) (*Store, error) {
	for _, ep := range endpoints {
		network, addr := dialTarget(ep)
		if err := netpolicy.CheckDial(ctx, network, addr); err != nil {
			return nil, fmt.Errorf("etcd kv: connect: %w", err)
		}
	}
	guarded := netpolicy.GuardDial(nil)
	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(dctx context.Context, addr string) (net.Conn, error) {
				network := "tcp"
				if target, ok := strings.CutPrefix(addr, "unix:"); ok {
					network, addr = "unix", target
				}
				return guarded(dctx, network, addr)
			}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("etcd kv: connect: %w", err)
	}
	return &Store{client: client, prefix: prefix}, nil
}

// dialTarget reduces an etcd endpoint to the network and address a dial
// would use, mirroring the client's own endpoint interpretation
// (clientv3 internal/endpoint, which is not importable).
//
// etcd accepts http(s):// and unix(s):// schemes as well as bare host:port.
// url.Parse cannot be used alone: it rejects "127.0.0.1:2379" outright and
// reads "localhost:2379" as scheme "localhost". Anything unrecognized is
// passed through as a TCP address, so an endpoint form not handled here is
// still policy-checked rather than silently exempted.
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
	for _, scheme := range []string{"http://", "https://"} {
		if rest, ok := strings.CutPrefix(ep, scheme); ok {
			// Trim any path/query the URL form may carry; the dial
			// only ever uses the authority.
			if i := strings.IndexAny(rest, "/?#"); i >= 0 {
				rest = rest[:i]
			}
			return "tcp", rest
		}
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
	return s.client.Close()
}

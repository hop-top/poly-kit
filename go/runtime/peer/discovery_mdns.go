package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// MDNSDiscoverer uses multicast DNS for peer discovery on the LAN.
type MDNSDiscoverer struct {
	service string
	self    PeerInfo
	server  *mdns.Server
	mu      sync.Mutex
}

// NewMDNSDiscoverer creates a discoverer for the given service type.
func NewMDNSDiscoverer(service string) *MDNSDiscoverer {
	return &MDNSDiscoverer{service: service}
}

// Announce registers this peer via mDNS TXT records.
func (d *MDNSDiscoverer) Announce(_ context.Context, self PeerInfo) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.self = self

	port := 0
	host := ""
	if len(self.Addrs) > 0 {
		h, p, err := net.SplitHostPort(self.Addrs[0])
		if err == nil {
			host = h
			fmt.Sscanf(p, "%d", &port)
		}
	}

	txt := d.encodeTXT(self)
	ips := []net.IP{net.ParseIP(host)}
	if host == "" || host == "localhost" {
		ips = []net.IP{net.IPv4(127, 0, 0, 1)}
	}

	svc, err := mdns.NewMDNSService(
		self.ID,
		d.service,
		"",
		"",
		port,
		ips,
		txt,
	)
	if err != nil {
		return fmt.Errorf("peer: mdns service: %w", err)
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		return fmt.Errorf("peer: mdns server: %w", err)
	}
	d.server = server
	return nil
}

// Browse scans for peers on the local network.
func (d *MDNSDiscoverer) Browse(_ context.Context) ([]PeerInfo, error) {
	entriesCh := make(chan *mdns.ServiceEntry, 16)
	var peers []PeerInfo
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for entry := range entriesCh {
			info := d.decodeTXT(entry)
			if info.ID != "" && info.ID != d.self.ID {
				mu.Lock()
				peers = append(peers, info)
				mu.Unlock()
			}
		}
	}()

	params := mdns.DefaultParams(d.service)
	params.Entries = entriesCh
	params.Timeout = 2 * time.Second

	if err := mdns.Query(params); err != nil {
		close(entriesCh)
		return nil, fmt.Errorf("peer: mdns browse: %w", err)
	}
	close(entriesCh)
	wg.Wait()

	return peers, nil
}

// Stop shuts down the mDNS server.
func (d *MDNSDiscoverer) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server != nil {
		return d.server.Shutdown()
	}
	return nil
}

func (d *MDNSDiscoverer) encodeTXT(info PeerInfo) []string {
	data, _ := json.Marshal(info)
	// TXT records have 255 byte limit; "p=" prefix uses 2, leaving 253 for data
	s := string(data)
	var parts []string
	for len(s) > 0 {
		end := 253
		if end > len(s) {
			end = len(s)
		}
		parts = append(parts, "p="+s[:end])
		s = s[end:]
	}
	return parts
}

func (d *MDNSDiscoverer) decodeTXT(entry *mdns.ServiceEntry) PeerInfo {
	var combined strings.Builder
	for _, txt := range entry.InfoFields {
		if strings.HasPrefix(txt, "p=") {
			combined.WriteString(txt[2:])
		}
	}
	var info PeerInfo
	_ = json.Unmarshal([]byte(combined.String()), &info)
	return info
}

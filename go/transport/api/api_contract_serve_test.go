package api_test

import (
	"encoding/asn1"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"crypto/x509/pkix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/internal/testpki"
)

// serveContract is the part of contracts/parity/api.json that covers
// server timeouts, the metrics endpoint, client certificates and the
// idempotency vocabulary. The Rust port runs the same tables.
type serveContract struct {
	Constants struct {
		Timeouts struct {
			ReadHeaderMS int64 `json:"read_header_ms"`
			ReadMS       int64 `json:"read_ms"`
			WriteMS      int64 `json:"write_ms"`
		} `json:"timeouts"`
		Metrics struct {
			DefaultPath    string `json:"default_path"`
			NotAllowedText string `json:"not_allowed_message"`
		} `json:"metrics"`
		Idempotency struct {
			KeyHeader      string         `json:"key_header"`
			ReplayedHeader string         `json:"replayed_header"`
			Codes          map[string]int `json:"codes"`
			DefaultTTLSecs int64          `json:"default_ttl_seconds"`
		} `json:"idempotency"`
	} `json:"constants"`
	Metrics []struct {
		Name          string `json:"name"`
		ConfigPath    string `json:"config_path"`
		Method        string `json:"method"`
		Path          string `json:"path"`
		Status        int    `json:"status"`
		Allow         string `json:"allow"`
		Code          string `json:"code"`
		CacheControl  string `json:"cache_control"`
		PassesThrough bool   `json:"passes_through"`
	} `json:"metrics"`
	ClientCert []struct {
		Name      string   `json:"name"`
		Principal string   `json:"principal"`
		CN        string   `json:"cn"`
		URIs      []string `json:"uris"`
		DNS       []string `json:"dns"`
		Emails    []string `json:"emails"`
		Org       []string `json:"org"`
		OrgUnit   []string `json:"org_unit"`
		Extension *struct {
			OID   string `json:"oid"`
			Value string `json:"value"`
		} `json:"extension"`
		TenantOID        string `json:"tenant_oid"`
		TenantSANPattern string `json:"tenant_san_pattern"`
		Want             struct {
			Subject string `json:"subject"`
			Tenant  string `json:"tenant"`
		} `json:"want"`
		Error bool `json:"error"`
	} `json:"client_cert"`
}

func readServeContract(t *testing.T) serveContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(apiContractPath))
	require.NoError(t, err)
	var c serveContract
	require.NoError(t, json.Unmarshal(raw, &c))
	return c
}

func TestAPIContractServeConstants(t *testing.T) {
	c := readServeContract(t).Constants
	assert.EqualValues(t, c.Timeouts.ReadHeaderMS, api.DefaultReadHeaderTimeout/time.Millisecond)
	assert.EqualValues(t, c.Timeouts.ReadMS, api.DefaultReadTimeout/time.Millisecond)
	assert.EqualValues(t, c.Timeouts.WriteMS, api.DefaultWriteTimeout/time.Millisecond)
	assert.Equal(t, c.Metrics.DefaultPath, api.DefaultMetricsPath)
	assert.Equal(t, c.Idempotency.KeyHeader, api.HeaderIdempotencyKey)
	assert.Equal(t, c.Idempotency.ReplayedHeader, api.HeaderIdempotentReplayed)
	assert.Equal(t, c.Idempotency.Codes[api.CodeIdempotencyConflict], http.StatusConflict)
	assert.Equal(t, c.Idempotency.Codes[api.CodeIdempotencyKeyReused], http.StatusUnprocessableEntity)
	assert.EqualValues(t, 86400, c.Idempotency.DefaultTTLSecs)
}

func TestAPIContractMetrics(t *testing.T) {
	for _, tt := range readServeContract(t).Metrics {
		t.Run(tt.Name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("next")) })
			h := api.MetricsRoute(next, api.MetricsConfig{
				Path: tt.ConfigPath,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte("exposition"))
				}),
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.Method, tt.Path, nil))
			if tt.PassesThrough {
				assert.Equal(t, "next", rec.Body.String())
				return
			}
			assert.Equal(t, tt.Status, rec.Code)
			assert.Equal(t, tt.CacheControl, rec.Header().Get("Cache-Control"))
			if tt.Status == http.StatusMethodNotAllowed {
				assert.Equal(t, tt.Allow, rec.Header().Get("Allow"))
				var body api.APIError
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, tt.Code, body.Code)
				assert.Equal(t, readServeContract(t).Constants.Metrics.NotAllowedText, body.Message)
			} else if tt.Method == http.MethodGet {
				assert.Equal(t, "exposition", rec.Body.String())
			}
		})
	}
}

func TestAPIContractClientCert(t *testing.T) {
	ca := testpki.NewCA(t, "ca")
	for _, tt := range readServeContract(t).ClientCert {
		t.Run(tt.Name, func(t *testing.T) {
			leaf := testpki.Leaf{CN: tt.CN, URIs: tt.URIs, DNS: tt.DNS, Emails: tt.Emails, Client: true}
			for _, o := range tt.Org {
				leaf.Names = append(leaf.Names, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 10}, Value: o})
			}
			for _, o := range tt.OrgUnit {
				leaf.Names = append(leaf.Names, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 11}, Value: o})
			}
			if e := tt.Extension; e != nil {
				val, err := asn1.Marshal(e.Value)
				require.NoError(t, err)
				leaf.Extensions = append(leaf.Extensions, pkix.Extension{Id: parseOID(t, e.OID), Value: val})
			}
			cfg := api.ClientCertConfig{Principal: tt.Principal}
			if tt.TenantOID != "" {
				cfg.TenantOID = parseOID(t, tt.TenantOID)
			}
			if tt.TenantSANPattern != "" {
				cfg.TenantSAN = regexp.MustCompile(tt.TenantSANPattern)
			}
			claims, err := api.ClientCertClaims(parsed(t, ca.Issue(t, leaf)), cfg)
			if tt.Error {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.Want.Subject, claims.Subject)
			assert.Equal(t, tt.Want.Tenant, claims.Tenant)
		})
	}
}

func parseOID(t *testing.T, s string) asn1.ObjectIdentifier {
	t.Helper()
	var oid asn1.ObjectIdentifier
	for _, p := range strings.Split(s, ".") {
		var n int
		for _, c := range p {
			n = n*10 + int(c-'0')
		}
		oid = append(oid, n)
	}
	return oid
}

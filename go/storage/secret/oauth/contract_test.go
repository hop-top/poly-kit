package oauth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"hop.top/kit/go/storage/secret/oauth"
)

// contractDir is contracts/oauth-token-v1, the language-neutral corpus every
// port replays.
var contractDir = filepath.Join("..", "..", "..", "..", "contracts", "oauth-token-v1")

func readContract(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractDir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.NewCompiler().Compile(filepath.Join(contractDir, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

type corpusCase struct {
	Name         string          `json:"name"`
	Valid        bool            `json:"valid"`
	Doc          json.RawMessage `json:"doc"`
	Raw          *string         `json:"raw"`
	Field        string          `json:"field"`
	SchemaGap    bool            `json:"schema_gap"`
	Encoded      json.RawMessage `json:"encoded"`
	ExpiresAtUTC string          `json:"expires_at_utc"`
}

type corpus struct {
	Comment []string     `json:"_comment"`
	Cases   []corpusCase `json:"cases"`
}

func (c corpusCase) bytes() []byte {
	if c.Raw != nil {
		return []byte(*c.Raw)
	}
	return []byte(c.Doc)
}

// sameJSON compares two JSON documents structurally.
func sameJSON(t *testing.T, got, want []byte) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got not JSON: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("want not JSON: %v", err)
	}
	return reflect.DeepEqual(g, w)
}

func wantDoc(c corpusCase) []byte {
	if len(c.Encoded) > 0 {
		return c.Encoded
	}
	return c.Doc
}

func checkInvalid(t *testing.T, c corpusCase, err error) {
	t.Helper()
	var ve *oauth.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if ve.Field != c.Field {
		t.Fatalf("field = %q, want %q (%v)", ve.Field, c.Field, err)
	}
}

func checkSchema(t *testing.T, s *jsonschema.Schema, c corpusCase) {
	t.Helper()
	if c.Raw != nil {
		return
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(c.Doc))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	err := s.Validate(v)
	switch {
	case c.Valid && err != nil:
		t.Fatalf("schema rejects valid doc: %v", err)
	case !c.Valid && !c.SchemaGap && err == nil:
		t.Fatal("schema accepts invalid doc")
	}
}

func TestContractGrantCorpus(t *testing.T) {
	var cs corpus
	readContract(t, "grant-corpus.json", &cs)
	if len(cs.Cases) == 0 {
		t.Fatal("empty corpus")
	}
	schema := compileSchema(t, "grant.schema.json")
	for _, c := range cs.Cases {
		t.Run(c.Name, func(t *testing.T) {
			checkSchema(t, schema, c)
			g, err := oauth.DecodeGrant(c.bytes())
			if !c.Valid {
				checkInvalid(t, c, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Validate(); err != nil {
				t.Fatal(err)
			}
			enc, err := oauth.EncodeGrant(g)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, enc, wantDoc(c)) {
				t.Fatalf("encode = %s, want %s", enc, wantDoc(c))
			}
			back, err := oauth.DecodeGrant(enc)
			if err != nil {
				t.Fatal(err)
			}
			again, err := oauth.EncodeGrant(back)
			if err != nil || !bytes.Equal(again, enc) || oauth.Digest(back) != oauth.Digest(g) {
				t.Fatalf("round trip: %v %s != %s", err, again, enc)
			}
		})
	}
}

func TestContractTokenCorpus(t *testing.T) {
	var cs corpus
	readContract(t, "token-corpus.json", &cs)
	if len(cs.Cases) == 0 {
		t.Fatal("empty corpus")
	}
	schema := compileSchema(t, "token.schema.json")
	for _, c := range cs.Cases {
		t.Run(c.Name, func(t *testing.T) {
			checkSchema(t, schema, c)
			tok, err := oauth.DecodeCachedToken(c.bytes())
			if !c.Valid {
				checkInvalid(t, c, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, c.ExpiresAtUTC)
			if err != nil || !tok.ExpiresAt.Equal(want) {
				t.Fatalf("expires_at = %v, want %s (%v)", tok.ExpiresAt, c.ExpiresAtUTC, err)
			}
			enc, err := oauth.EncodeCachedToken(tok)
			if err != nil {
				t.Fatal(err)
			}
			var w map[string]any
			if err := json.Unmarshal(wantDoc(c), &w); err != nil {
				t.Fatal(err)
			}
			w["expires_at"] = c.ExpiresAtUTC
			wb, _ := json.Marshal(w)
			if !sameJSON(t, enc, wb) {
				t.Fatalf("encode = %s, want %s", enc, wb)
			}
		})
	}
}

func TestContractDigestVectors(t *testing.T) {
	var f struct {
		Comment []string `json:"_comment"`
		Vectors []struct {
			Name      string          `json:"name"`
			Grant     json.RawMessage `json:"grant"`
			Canonical string          `json:"canonical"`
			Digest    string          `json:"digest"`
			Ref       string          `json:"ref"`
			TokenKey  string          `json:"token_key"`
			Note      string          `json:"note"`
		} `json:"vectors"`
	}
	readContract(t, "digest.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	schema := compileSchema(t, "grant.schema.json")
	for _, v := range f.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			checkSchema(t, schema, corpusCase{Valid: true, Doc: v.Grant})
			g, err := oauth.DecodeGrant(v.Grant)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(v.Canonical))
			if h := hex.EncodeToString(sum[:])[:16]; h != v.Digest {
				t.Fatalf("recorded canonical hashes to %s, digest says %s", h, v.Digest)
			}
			if got := oauth.Digest(g); got != v.Digest {
				t.Fatalf("Digest = %s, want %s", got, v.Digest)
			}
			if got := oauth.TokenKey(v.Ref, g); got != v.TokenKey {
				t.Fatalf("TokenKey = %s, want %s", got, v.TokenKey)
			}
		})
	}
}

func TestContractUmaEnvelopePairs(t *testing.T) {
	var f struct {
		Comment []string `json:"_comment"`
		Pairs   []struct {
			Name  string `json:"name"`
			Token struct {
				AccessToken string `json:"access_token"`
				ExpiresAt   string `json:"expires_at"`
				User        string `json:"user"`
				AuthzID     string `json:"authzid"`
			} `json:"token"`
			Envelope json.RawMessage `json:"envelope"`
		} `json:"pairs"`
	}
	readContract(t, "uma-envelope.json", &f)
	if len(f.Pairs) == 0 {
		t.Fatal("no pairs")
	}
	schema := compileSchema(t, "token.schema.json")
	for _, p := range f.Pairs {
		t.Run(p.Name, func(t *testing.T) {
			checkSchema(t, schema, corpusCase{Valid: true, Doc: p.Envelope})
			exp, err := time.Parse(time.RFC3339Nano, p.Token.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			c := oauth.CachedToken{
				Token: oauth.Token{AccessToken: p.Token.AccessToken, ExpiresAt: exp},
				User:  p.Token.User, AuthzID: p.Token.AuthzID,
			}
			enc, err := oauth.EncodeCachedToken(c)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, enc, p.Envelope) {
				t.Fatalf("encode = %s, want %s", enc, p.Envelope)
			}
			dec, err := oauth.DecodeCachedToken(p.Envelope)
			if err != nil {
				t.Fatal(err)
			}
			if dec.AccessToken != c.AccessToken || !dec.ExpiresAt.Equal(exp) || dec.User != c.User || dec.AuthzID != c.AuthzID {
				t.Fatalf("decode = %+v, want %+v", dec, c)
			}
		})
	}
}

func TestContractErrorTaxonomy(t *testing.T) {
	var f struct {
		Comment []string `json:"_comment"`
		Classes []struct {
			Name    string `json:"name"`
			Meaning string `json:"meaning"`
		} `json:"classes"`
		OAuthErrors []struct {
			Code   string            `json:"code"`
			Class  string            `json:"class"`
			ByKind map[string]string `json:"by_kind"`
		} `json:"oauth_errors"`
		Other []struct {
			Condition string `json:"condition"`
			Class     string `json:"class"`
		} `json:"other"`
		Unclassified []struct {
			Condition string `json:"condition"`
		} `json:"unclassified"`
		Validation []struct {
			Condition string `json:"condition"`
		} `json:"validation"`
	}
	readContract(t, "errors.json", &f)
	if len(f.Unclassified) == 0 || len(f.Validation) == 0 {
		t.Error("errors.json: unclassified and validation entries required")
	}
	sentinels := map[string]error{
		"credential_rejected": oauth.ErrCredentialRejected,
		"needs_reconsent":     oauth.ErrNeedsReconsent,
		"not_writable":        oauth.ErrNotWritable,
		"transient":           oauth.ErrTransient,
	}
	if len(f.Classes) != len(sentinels) {
		t.Fatalf("%d classes, want %d", len(f.Classes), len(sentinels))
	}
	for _, c := range f.Classes {
		if sentinels[c.Name] == nil || c.Meaning == "" {
			t.Errorf("class %q unknown or without meaning", c.Name)
		}
	}
	known := func(name string) bool { return sentinels[name] != nil }
	want := map[string]struct{ def, refresh string }{
		"invalid_client":       {"credential_rejected", "credential_rejected"},
		"unauthorized_client":  {"credential_rejected", "credential_rejected"},
		"invalid_grant":        {"credential_rejected", "needs_reconsent"},
		"interaction_required": {"credential_rejected", "needs_reconsent"},
		"consent_required":     {"credential_rejected", "needs_reconsent"},
		"login_required":       {"credential_rejected", "needs_reconsent"},
	}
	if len(f.OAuthErrors) != len(want) {
		t.Fatalf("%d oauth_errors, want %d", len(f.OAuthErrors), len(want))
	}
	for _, e := range f.OAuthErrors {
		w, ok := want[e.Code]
		if !ok || !known(e.Class) || e.Class != w.def {
			t.Errorf("oauth_errors %q: class %q", e.Code, e.Class)
		}
		got := e.Class
		if k, ok := e.ByKind[string(oauth.RefreshToken)]; ok {
			got = k
		}
		if got != w.refresh || len(e.ByKind) > 1 {
			t.Errorf("oauth_errors %q: refresh_token maps to %q, want %q", e.Code, got, w.refresh)
		}
	}
	for _, o := range f.Other {
		if !known(o.Class) || o.Condition == "" {
			t.Errorf("other %q: class %q", o.Condition, o.Class)
		}
	}
	if !errors.Is(&oauth.NotWritableError{Backend: "b"}, oauth.ErrNotWritable) {
		t.Error("NotWritableError does not match ErrNotWritable")
	}
}

package bus

import (
	"strings"
	"testing"
)

func TestKitOffsetIsReserved(t *testing.T) {
	e := NewEvent("acme.crm.deal.created", "crm", nil)
	e.Extensions = map[string]any{OffsetExtension: "1"}
	if _, err := ToCloudEvent(e); err == nil || !strings.Contains(err.Error(), "kitoffset: reserved for the stream offset") {
		t.Fatalf("ToCloudEvent err = %v", err)
	}
	if _, err := UnmarshalCloudEvent([]byte(`{"specversion":"1.0","id":"x","source":"s","type":"t","kitoffset":"1"}`)); err == nil || !strings.Contains(err.Error(), "kitoffset: reserved for the stream offset") {
		t.Fatalf("FromCloudEvent err = %v", err)
	}
	// A network frame strips it before the reserved check.
	ev, _, err := decodeFrame([]byte(`{"specversion":"1.0","id":"x","source":"s","type":"t","kitoffset":"1"}`))
	if err != nil || ev.Extensions != nil {
		t.Fatalf("frame decode = %+v, %v", ev, err)
	}
}

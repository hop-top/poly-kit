package bus

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvent_UnmarshalRejectsUnencodableOffset(t *testing.T) {
	for _, off := range []string{"+24:00", "-24:00", "+23:60", "+24:01", "+00:60"} {
		var e Event
		err := json.Unmarshal([]byte(`{"timestamp":"2024-05-01T12:00:00`+off+`"}`), &e)
		if err == nil || !strings.Contains(err.Error(), "invalid zone offset") {
			t.Errorf("%s: err = %v, want invalid zone offset", off, err)
		}
	}
	for _, ts := range []string{"2024-05-01T12:00:00Z", "2024-05-01T12:00:00+23:59", "2024-05-01T12:00:00.5-07:00"} {
		var e Event
		if err := json.Unmarshal([]byte(`{"timestamp":"`+ts+`"}`), &e); err != nil {
			t.Errorf("%s: %v", ts, err)
		}
	}
}

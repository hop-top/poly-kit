package cli

import (
	"bytes"
	"testing"
)

func TestExitCode_Values(t *testing.T) {
	tests := []struct {
		code ExitCode
		want int
	}{
		{ExitOK, 0},
		{ExitError, 1},
		{ExitUsage, 2},
		{ExitNotFound, 3},
		{ExitConflict, 4},
		{ExitAuth, 5},
		{ExitPermission, 6},
		{ExitTimeout, 7},
		{ExitCancelled, 8},
	}
	for _, tt := range tests {
		if int(tt.code) != tt.want {
			t.Errorf("ExitCode %d != expected %d", tt.code, tt.want)
		}
	}
}

func TestStreamWriter_SeparatesDataAndHuman(t *testing.T) {
	var dataBuf, humanBuf bytes.Buffer
	sw := &StreamWriter{
		Data:  &dataBuf,
		Human: &humanBuf,
		IsTTY: false,
	}

	_, _ = sw.Data.Write([]byte(`{"status":"ok"}`))
	_, _ = sw.Human.Write([]byte("Processing..."))

	if dataBuf.String() != `{"status":"ok"}` {
		t.Errorf("Data got %q", dataBuf.String())
	}
	if humanBuf.String() != "Processing..." {
		t.Errorf("Human got %q", humanBuf.String())
	}
}

func TestNewStreamWriter_Defaults(t *testing.T) {
	sw := NewStreamWriter()
	if sw.Data == nil {
		t.Fatal("Data writer is nil")
	}
	if sw.Human == nil {
		t.Fatal("Human writer is nil")
	}
}

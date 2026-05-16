package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"
)

func TestCommandsExist(t *testing.T) {
	root := cli.New(cli.Config{
		Name:            "relay",
		Version:         "0.1.0",
		Short:           "test",
		DisableValidate: true,
	})
	b := bus.New()
	defer b.Close(context.Background())
	root.Cmd.AddCommand(listenCmd(b), connectCmd(b), publishCmd(b), subscribeCmd(b))

	for _, name := range []string{"listen", "connect", "publish", "subscribe"} {
		found := false
		for _, c := range root.Cmd.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		assert.True(t, found, "command %q should exist", name)
	}
}

func TestBusRoundtrip(t *testing.T) {
	b := bus.New()
	defer b.Close(context.Background())

	received := make(chan bus.Event, 1)
	unsub := b.Subscribe("test.#", func(_ context.Context, e bus.Event) error {
		received <- e
		return nil
	})
	defer unsub()

	e := bus.NewEvent("test.hello", "test", "world")
	require.NoError(t, b.Publish(context.Background(), e))

	select {
	case got := <-received:
		assert.Equal(t, bus.Topic("test.hello"), got.Topic)
		assert.Equal(t, "world", got.Payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

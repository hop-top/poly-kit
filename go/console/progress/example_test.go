package progress_test

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"hop.top/kit/go/console/progress"
)

func ExampleFromContext() {
	var buf bytes.Buffer
	ctx := progress.WithReporter(context.Background(), progress.JSONL(&buf))

	r := progress.FromContext(ctx)
	r.Emit(ctx, progress.Event{
		Phase: "download",
		At:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Item:  "kit.tar.gz",
		Bytes: 512 * 1024,
		Total: 2048 * 1024,
	})
	fmt.Print(buf.String())

	// Output:
	// {"phase":"download","at":"2026-01-02T03:04:05Z","item":"kit.tar.gz","bytes":524288,"total":2097152}
}

func ExampleHuman() {
	var buf bytes.Buffer
	r := progress.Human(&buf)
	ok := true

	r.Emit(context.Background(), progress.Event{Phase: "resolve", Item: "hop.top/kit"})
	r.Emit(context.Background(), progress.Event{
		Phase: "download", Item: "kit.tar.gz", Bytes: 512 * 1024, Total: 2048 * 1024,
	})
	r.Emit(context.Background(), progress.Event{Phase: "verify", OK: &ok})
	fmt.Print(buf.String())

	// Output:
	// [resolve] hop.top/kit
	// [download] kit.tar.gz (512.0 KiB/2048.0 KiB)
	// [verify] ok
}

//go:build s3

package s3

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	bucket := os.Getenv("S3_TEST_BUCKET")
	if bucket == "" {
		t.Skip("S3_TEST_BUCKET not set")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return New(s3.NewFromConfig(cfg), bucket, "blob-test/")
}

func TestPutGetDeleteExists(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)

	const key = "integration/hello.txt"
	body := []byte("hello world")

	if err := st.Put(ctx, key, bytes.NewReader(body), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}

	ok, err := st.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !ok {
		t.Fatal("expected object to exist")
	}

	rc, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("read: %v", err)
	}
	rc.Close()
	if buf.String() != "hello world" {
		t.Fatalf("got %q", buf.String())
	}

	if err := st.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	ok, err = st.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists after delete: %v", err)
	}
	if ok {
		t.Fatal("expected object to not exist after delete")
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)

	const prefix = "integration/list/"
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := st.Put(ctx, prefix+name, bytes.NewReader([]byte("x")), "text/plain"); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		for _, name := range []string{"a.txt", "b.txt"} {
			_ = st.Delete(ctx, prefix+name)
		}
	})

	objs, err := st.List(ctx, prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) < 2 {
		t.Fatalf("expected >=2 objects, got %d", len(objs))
	}
}

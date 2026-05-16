package tidb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startMySQL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "mysql:8",
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "test",
			"MYSQL_DATABASE":      "testdb",
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("ready for connections").WithOccurrence(2),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(120 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("skipping: could not start mysql container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "3306")
	require.NoError(t, err)
	return fmt.Sprintf("root:test@tcp(%s:%s)/testdb", host, port.Port())
}

func TestIntegration_VersionRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	d, err := New(dsn, "test_schema_versions", WithName("myschema"))
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer d.Close()

	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "", v)

	require.NoError(t, d.SetVersion("1.0.0"))
	v, err = d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)

	require.NoError(t, d.SetVersion("2.0.0"))
	v, err = d.Version()
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", v)

	d.db.Exec("DROP TABLE test_schema_versions")
}

func TestIntegration_MultipleSchemas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	d1, err := New(dsn, "test_multi_versions", WithName("schema_a"))
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer d1.Close()

	d2, err := New(dsn, "test_multi_versions", WithName("schema_b"))
	require.NoError(t, err)
	defer d2.Close()

	require.NoError(t, d1.SetVersion("1.0.0"))
	require.NoError(t, d2.SetVersion("3.0.0"))

	v1, err := d1.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v1)

	v2, err := d2.Version()
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", v2)

	d1.db.Exec("DROP TABLE test_multi_versions")
}

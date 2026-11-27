package postgres

// Option configures a Postgres driver.
type Option func(*Driver)

// WithName overrides the schema name (default "postgres"). It is the
// schema_name row key and part of the advisory lock key.
func WithName(name string) Option {
	return func(d *Driver) { d.name = name }
}

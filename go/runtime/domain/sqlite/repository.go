package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/storage/sqlstore"
)

// sortPattern validates Sort values: column name optionally followed by
// ASC or DESC. Only letters and underscores are allowed for column names.
var sortPattern = regexp.MustCompile(`^[a-zA-Z_]+( (?i:ASC|DESC))?$`)

// ScanFunc converts a sql.Row into an entity of type T.
type ScanFunc[T domain.Entity] func(*sql.Row) (T, error)

// ScanRowsFunc converts a sql.Rows cursor into an entity of type T.
type ScanRowsFunc[T domain.Entity] func(*sql.Rows) (T, error)

// BindFunc returns ordered column names and their values for an entity,
// suitable for INSERT or UPDATE statements.
type BindFunc[T domain.Entity] func(T) (cols []string, vals []any)

// PKFunc extracts primary key column names and values from an entity.
type PKFunc[T domain.Entity] func(T) (cols []string, vals []any)

// RepoOption configures a SQLiteRepository.
type RepoOption[T domain.Entity] func(*SQLiteRepository[T])

// WithPK sets a custom primary key extractor.
// Default is single-column "id" derived from entity.GetID().
func WithPK[T domain.Entity](pk PKFunc[T]) RepoOption[T] {
	return func(r *SQLiteRepository[T]) {
		if pk == nil {
			return
		}
		r.pk = pk
	}
}

// SQLiteRepository is a generic Repository backed by a sqlstore.Store.
type SQLiteRepository[T domain.Entity] struct {
	db       *sql.DB
	table    string
	scan     ScanFunc[T]
	scanRows ScanRowsFunc[T]
	bind     BindFunc[T]
	pk       PKFunc[T]
}

// defaultPK returns the single-column "id" primary key.
func defaultPK[T domain.Entity](entity T) ([]string, []any) {
	return []string{"id"}, []any{entity.GetID()}
}

// NewSQLiteRepository creates a repository for the given table.
//
// The store's DB() is used for all queries. The caller must provide:
//   - scan: converts a single *sql.Row to T
//   - scanRows: converts a *sql.Rows cursor to T
//   - bind: extracts column names + values from T for INSERT/UPDATE
//
// Optional RepoOption values (e.g. WithPK) may be appended.
func NewSQLiteRepository[T domain.Entity](
	store *sqlstore.Store,
	table string,
	scan ScanFunc[T],
	scanRows ScanRowsFunc[T],
	bind BindFunc[T],
	opts ...RepoOption[T],
) *SQLiteRepository[T] {
	r := &SQLiteRepository[T]{
		db:       store.DB(),
		table:    table,
		scan:     scan,
		scanRows: scanRows,
		bind:     bind,
		pk:       defaultPK[T],
	}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(r)
	}
	return r
}

// Create inserts a new entity. Returns ErrConflict on primary key collision.
func (r *SQLiteRepository[T]) Create(ctx context.Context, entity *T) error {
	cols, vals := r.bind(*entity)
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		r.table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	_, err := r.db.ExecContext(ctx, q, vals...)
	if err != nil && isUniqueViolation(err) {
		return fmt.Errorf("%w: %s", domain.ErrConflict, err)
	}
	return err
}

// Get retrieves an entity by primary key (first bind column = "id").
func (r *SQLiteRepository[T]) Get(ctx context.Context, id string) (*T, error) {
	q := fmt.Sprintf("SELECT * FROM %s WHERE id = ?", r.table)
	row := r.db.QueryRowContext(ctx, q, id)
	entity, err := r.scan(row)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entity, nil
}

// List queries entities with optional pagination, sorting, and search.
func (r *SQLiteRepository[T]) List(ctx context.Context, qp domain.Query) ([]T, error) {
	var (
		where string
		args  []any
		parts []string
	)
	base := fmt.Sprintf("SELECT * FROM %s", r.table)

	if qp.Search != "" {
		where = " WHERE id LIKE ?"
		args = append(args, "%"+qp.Search+"%")
	}

	parts = append(parts, base+where)

	if qp.Sort != "" {
		if !sortPattern.MatchString(qp.Sort) {
			return nil, fmt.Errorf(
				"%w: invalid sort %q (must be column_name [ASC|DESC])",
				domain.ErrValidation, qp.Sort,
			)
		}
		parts = append(parts, fmt.Sprintf("ORDER BY %s", qp.Sort))
	}
	if qp.Limit > 0 {
		parts = append(parts, fmt.Sprintf("LIMIT %d", qp.Limit))
	}
	if qp.Offset > 0 {
		parts = append(parts, fmt.Sprintf("OFFSET %d", qp.Offset))
	}

	q := strings.Join(parts, " ")
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []T
	for rows.Next() {
		entity, err := r.scanRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entity)
	}
	return result, rows.Err()
}

// pkWhere builds a WHERE clause and args from the PKFunc for entity.
func (r *SQLiteRepository[T]) pkWhere(entity T) (string, []any, error) {
	cols, vals := r.pk(entity)
	if len(cols) == 0 {
		return "", nil, fmt.Errorf("%w: PKFunc returned no columns", domain.ErrValidation)
	}
	if len(cols) != len(vals) {
		return "", nil, fmt.Errorf(
			"%w: PKFunc returned %d columns but %d values",
			domain.ErrValidation, len(cols), len(vals),
		)
	}
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprintf("%s = ?", c)
	}
	return strings.Join(parts, " AND "), vals, nil
}

// Update replaces an existing entity. Returns ErrNotFound if absent.
func (r *SQLiteRepository[T]) Update(ctx context.Context, entity *T) error {
	pkCols, _ := r.pk(*entity)
	pkSet := make(map[string]struct{}, len(pkCols))
	for _, c := range pkCols {
		pkSet[c] = struct{}{}
	}

	cols, vals := r.bind(*entity)

	// Validate that bind returns all PK columns.
	bindSet := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		bindSet[c] = struct{}{}
	}
	for _, c := range pkCols {
		if _, ok := bindSet[c]; !ok {
			return fmt.Errorf(
				"%w: bind did not return pk column %q",
				domain.ErrValidation, c,
			)
		}
	}

	sets := make([]string, 0, len(cols))
	var updateVals []any
	for i, col := range cols {
		if _, isPK := pkSet[col]; isPK {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = ?", col))
		updateVals = append(updateVals, vals[i])
	}
	if len(sets) == 0 {
		return fmt.Errorf("%w: no non-PK columns to update", domain.ErrValidation)
	}

	whereClause, whereArgs, err := r.pkWhere(*entity)
	if err != nil {
		return err
	}
	updateVals = append(updateVals, whereArgs...)
	q := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		r.table, strings.Join(sets, ", "), whereClause)

	res, err := r.db.ExecContext(ctx, q, updateVals...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete removes an entity by ID. Returns ErrNotFound if absent.
func (r *SQLiteRepository[T]) Delete(ctx context.Context, id string) error {
	q := fmt.Sprintf("DELETE FROM %s WHERE id = ?", r.table)
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetByPK retrieves an entity using all primary key columns.
// The key parameter only needs PK fields populated.
func (r *SQLiteRepository[T]) GetByPK(
	ctx context.Context, key T,
) (*T, error) {
	whereClause, whereArgs, err := r.pkWhere(key)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("SELECT * FROM %s WHERE %s", r.table, whereClause)
	row := r.db.QueryRowContext(ctx, q, whereArgs...)
	entity, err := r.scan(row)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entity, nil
}

// DeleteByPK removes an entity using all primary key columns.
func (r *SQLiteRepository[T]) DeleteByPK(
	ctx context.Context, key T,
) error {
	whereClause, whereArgs, err := r.pkWhere(key)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", r.table, whereClause)
	res, err := r.db.ExecContext(ctx, q, whereArgs...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// isUniqueViolation checks if the error is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

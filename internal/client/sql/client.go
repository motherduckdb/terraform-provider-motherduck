package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type Config struct {
	Token           string
	Database        string
	AttachMode      string
	CustomUserAgent string
}

type Client struct {
	db      *sql.DB
	semOnce sync.Once
	sem     chan struct{}
}

type Row struct {
	row     *sql.Row
	err     error
	release func()
	once    sync.Once
}

// RowScanner is the narrow result contract consumed by provider resources and
// data sources. Keeping callers on this interface lets hermetic contract tests
// supply deterministic rows without replacing the production database/sql
// implementation.
type RowScanner interface {
	Scan(dest ...any) error
}

// errNULQuery is returned instead of sending a statement that contains a NUL
// byte. The DuckDB C API truncates statements at the first NUL, so the parser
// would see a different statement than the caller built. The message omits the
// statement because the truncated text can contain part of a secret value.
var errNULQuery = errors.New("SQL statement contains a NUL byte and was not sent")

func checkQuery(query string) error {
	if strings.IndexByte(query, 0) >= 0 {
		return errNULQuery
	}
	return nil
}

// sqlSession is the subset of database/sql shared by *sql.DB and *sql.Conn.
type sqlSession interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type contextConnector struct {
	*duckdb.Connector
	initialize func(context.Context, driver.ExecerContext) error
}

// oneTimeInitializer bootstraps the shared DuckDB database exactly once. The
// MotherDuck token setting is database initialization state and cannot be set
// again on a pooled reconnect after an md: database has been attached. A
// failed initialization remains retryable with the next connection context.
type oneTimeInitializer struct {
	mu          sync.Mutex
	initialized bool
	next        int
	queries     []string
	token       string
}

func (i *oneTimeInitializer) run(ctx context.Context, execer driver.ExecerContext) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.initialized {
		return nil
	}
	initCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for i.next < len(i.queries) {
		query := i.queries[i.next]
		tflog.Debug(initCtx, "running MotherDuck SQL boot query", map[string]any{"query": redactToken(query, i.token)})
		if _, err := execer.ExecContext(initCtx, query, nil); err != nil {
			// A canceled ATTACH can complete remotely before reporting an
			// error. Retry the immutable attach statement idempotently so the
			// next connection does not need to guess whether it attached.
			if strings.HasPrefix(query, "ATTACH ") && strings.Contains(err.Error(), "Your MotherDuck databases are already attached") {
				retryQuery := strings.Replace(query, "ATTACH ", "ATTACH IF NOT EXISTS ", 1)
				if _, retryErr := execer.ExecContext(initCtx, retryQuery, nil); retryErr == nil {
					i.next++
					continue
				}
			}
			return fmt.Errorf("running boot query %q: %s", redactToken(query, i.token), redactToken(err.Error(), i.token))
		}
		i.next++
	}
	i.initialized = true
	return nil
}

// Connect supplies each pool connection's current operation context to the
// MotherDuck boot queries instead of retaining the first operation's context.
func (c *contextConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		initErr := errors.New("DuckDB connection does not support context-aware initialization")
		if closeErr := conn.Close(); closeErr != nil {
			return nil, errors.Join(initErr, closeErr)
		}
		return nil, initErr
	}
	if err := c.initialize(ctx, execer); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	return conn, nil
}

// Scan releases the client for the next operation. Releasing is guarded so a
// second Scan on the same Row cannot release another operation's admission.
func (r *Row) Scan(dest ...any) error {
	if r.release != nil {
		defer r.once.Do(r.release)
	}
	if r.err != nil {
		return r.err
	}
	return r.row.Scan(dest...)
}

func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return &Client{}, nil
	}

	dsn := ":memory:"
	if cfg.CustomUserAgent != "" {
		dsn += "?custom_user_agent=" + url.QueryEscape(cfg.CustomUserAgent)
	}

	duckdbConnector, err := duckdb.NewConnector(dsn, nil)
	if err != nil {
		return nil, err
	}
	initialize := &oneTimeInitializer{queries: bootQueries(token, cfg), token: token}
	connector := &contextConnector{Connector: duckdbConnector, initialize: initialize.run}

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Client{db: db}, nil
}

// bootQueries returns the one-time MotherDuck initialization statements for a
// trimmed token and the configured attach target.
func bootQueries(token string, cfg Config) []string {
	queries := []string{
		"INSTALL motherduck",
		"LOAD motherduck",
		"SET motherduck_token = " + sqlbuild.StringLiteral(token),
	}
	if cfg.AttachMode != "" {
		queries = append(queries, "SET motherduck_attach_mode = "+sqlbuild.StringLiteral(cfg.AttachMode))
	}
	if cfg.Database != "" {
		queries = append(queries, "ATTACH "+sqlbuild.StringLiteral("md:"+cfg.Database))
	} else {
		// Initialize the default workspace explicitly. Without this attach,
		// the first md_user() query can be answered by local DuckDB as
		// "duckdb" even though the MotherDuck token is configured.
		queries = append(queries, "ATTACH "+sqlbuild.StringLiteral("md:"))
	}
	return queries
}

func (c *Client) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *Client) Available() bool {
	return c != nil && c.db != nil
}

// acquire admits one SQL operation at a time on the shared connection.
// Waiting observes ctx, so an operation whose timeout expires or that is
// canceled while queued behind another statement returns promptly.
func (c *Client) acquire(ctx context.Context) error {
	c.semOnce.Do(func() { c.sem = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() {
	<-c.sem
}

func (c *Client) Exec(ctx context.Context, query string, args ...any) error {
	if !c.Available() {
		return ErrMissingToken
	}
	if err := checkQuery(query); err != nil {
		return err
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return execOn(ctx, c.db, query, args...)
}

func execOn(ctx context.Context, session sqlSession, query string, args ...any) error {
	if err := checkQuery(query); err != nil {
		return err
	}
	_, err := session.ExecContext(ctx, query, args...)
	return err
}

func (c *Client) AttachDatabase(ctx context.Context, database string) error {
	if strings.TrimSpace(database) == "" {
		return nil
	}
	return c.Exec(ctx, "ATTACH IF NOT EXISTS "+sqlbuild.StringLiteral("md:"+database))
}

func (c *Client) WithDatabaseUse(ctx context.Context, database string, fn func(exec func(string, ...any) error) error) error {
	if !c.Available() {
		return ErrMissingToken
	}
	if strings.TrimSpace(database) == "" {
		return fn(func(query string, args ...any) error {
			return c.Exec(ctx, query, args...)
		})
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()

	// A dedicated connection lets the scope discard it when the previous
	// database cannot be restored, so later operations never inherit the
	// temporary USE target from the pooled connection.
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	discard := false
	defer func() {
		if discard {
			// Returning driver.ErrBadConn from Raw makes database/sql close
			// the connection instead of returning it to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}()

	previous, err := scalarStringOn(ctx, conn, "SELECT current_database()")
	if err != nil {
		return fmt.Errorf("read current database: %w", err)
	}
	if err := execOn(ctx, conn, "USE "+sqlbuild.QuoteIdentifier(database)); err != nil {
		return err
	}
	runErr := fn(func(query string, args ...any) error {
		return execOn(ctx, conn, query, args...)
	})
	// Restoration must survive cancellation of the operation that changed USE.
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	restoreErr := execOn(restoreCtx, conn, "USE "+sqlbuild.QuoteIdentifier(previous))
	if restoreErr != nil {
		discard = true
		return errors.Join(runErr, fmt.Errorf("restore previous database %q: %w", previous, restoreErr))
	}
	return runErr
}

func (c *Client) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if !c.Available() {
		return &Row{err: ErrMissingToken}
	}
	if err := checkQuery(query); err != nil {
		return &Row{err: err}
	}
	if err := c.acquire(ctx); err != nil {
		return &Row{err: err}
	}
	return &Row{
		row:     c.db.QueryRowContext(ctx, query, args...),
		release: c.release,
	}
}

func (c *Client) QueryRowsJSON(ctx context.Context, query string, args ...any) (result string, err error) {
	if !c.Available() {
		return "", ErrMissingToken
	}
	if err := checkQuery(query); err != nil {
		return "", err
	}
	if err := c.acquire(ctx); err != nil {
		return "", err
	}
	defer c.release()
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return "", err
	}
	out := make([]map[string]any, 0)
	values := make([]any, len(cols))
	targets := make([]any, len(cols))
	valueTypes := make([]*columnType, len(cols))
	seen := make(map[string]struct{}, len(cols))
	for i := range cols {
		cols[i] = strings.ToLower(cols[i])
		if _, duplicate := seen[cols[i]]; duplicate {
			return "", fmt.Errorf("query returned duplicate column name %q after case folding", cols[i])
		}
		seen[cols[i]] = struct{}{}
		targets[i] = &values[i]
		valueTypes[i] = parseColumnType(columnTypes[i].DatabaseTypeName())
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeTyped(values[i], valueTypes[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	if !c.Available() {
		return "", ErrMissingToken
	}
	if err := checkQuery(query); err != nil {
		return "", err
	}
	if err := c.acquire(ctx); err != nil {
		return "", err
	}
	defer c.release()
	return scalarStringOn(ctx, c.db, query, args...)
}

func scalarStringOn(ctx context.Context, session sqlSession, query string, args ...any) (string, error) {
	var value sql.NullString
	if err := session.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		return "", err
	}
	return value.String, nil
}

func (c *Client) Exists(ctx context.Context, query string, args ...any) (bool, error) {
	if !c.Available() {
		return false, ErrMissingToken
	}
	if err := checkQuery(query); err != nil {
		return false, err
	}
	if err := c.acquire(ctx); err != nil {
		return false, err
	}
	defer c.release()
	var count int
	if err := c.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// normalizeValue converts one scanned DuckDB value into a JSON-friendly value
// using the column's DuckDB type name.
func normalizeValue(value any, databaseTypeName string) any {
	return normalizeTyped(value, parseColumnType(databaseTypeName))
}

// normalizeTyped converts scanned DuckDB values into JSON-friendly values,
// recursing through lists, structs, maps, and unions. The column type tells
// UUID bytes apart from BLOB bytes at any nesting depth.
func normalizeTyped(value any, t *columnType) any {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		if t.is("UUID") && len(v) == 16 {
			return formatUUIDBytes(v)
		}
		return hex.EncodeToString(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case duckdb.UUID:
		return formatUUIDBytes(v[:])
	case *duckdb.UUID:
		if v == nil {
			return nil
		}
		return formatUUIDBytes(v[:])
	case duckdb.Decimal:
		if v.Value == nil {
			return nil
		}
		// json.Number keeps the exact decimal text instead of rounding
		// through float64.
		return json.Number(v.String())
	case duckdb.Interval:
		return formatInterval(v)
	case duckdb.Union:
		return duckdb.Union{Tag: v.Tag, Value: normalizeTyped(v.Value, t.field(v.Tag))}
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeTyped(item, t.element())
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeTyped(item, t.field(key))
		}
		return out
	case duckdb.OrderedMap:
		var out duckdb.OrderedMap
		keys, values := v.Keys(), v.Values()
		for i := range keys {
			out.Set(comparableKey(normalizeTyped(keys[i], t.mapKey())), normalizeTyped(values[i], t.mapValue()))
		}
		return out
	case duckdb.Map:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[fmt.Sprint(normalizeTyped(key, t.mapKey()))] = normalizeTyped(item, t.mapValue())
		}
		return out
	default:
		if _, err := json.Marshal(v); err == nil {
			return v
		}
		return fmt.Sprint(v)
	}
}

// comparableKey keeps normalized map keys usable as OrderedMap keys, which
// are compared with ==.
func comparableKey(key any) any {
	if key == nil || reflect.TypeOf(key).Comparable() {
		return key
	}
	return fmt.Sprint(key)
}

// formatInterval renders an interval the way DuckDB casts INTERVAL to
// VARCHAR, for example "7 days" or "1 year 2 months 03:04:05.5".
func formatInterval(v duckdb.Interval) string {
	var parts []string
	unit := func(n int64, name string) string {
		if n == 1 || n == -1 {
			return fmt.Sprintf("%d %s", n, name)
		}
		return fmt.Sprintf("%d %ss", n, name)
	}
	if v.Months != 0 {
		years := v.Months / 12
		months := v.Months - years*12
		if years != 0 {
			parts = append(parts, unit(int64(years), "year"))
		}
		if months != 0 {
			parts = append(parts, unit(int64(months), "month"))
		}
	}
	if v.Days != 0 {
		parts = append(parts, unit(int64(v.Days), "day"))
	}
	if v.Micros != 0 {
		sign := ""
		micros := uint64(v.Micros) // #nosec G115 -- only used when v.Micros is positive.
		if v.Micros < 0 {
			sign = "-"
			// Negating in int64 wraps for the minimum value, and the
			// unsigned conversion of that result is still the exact magnitude.
			micros = uint64(-v.Micros) // #nosec G115 -- see the comment above.
		}
		const microsPerSecond = uint64(time.Second / time.Microsecond)
		hours := micros / (3600 * microsPerSecond)
		micros -= hours * 3600 * microsPerSecond
		minutes := micros / (60 * microsPerSecond)
		micros -= minutes * 60 * microsPerSecond
		seconds := micros / microsPerSecond
		micros -= seconds * microsPerSecond
		clock := fmt.Sprintf("%s%02d:%02d:%02d", sign, hours, minutes, seconds)
		if micros != 0 {
			clock += "." + strings.TrimRight(fmt.Sprintf("%06d", micros), "0")
		}
		parts = append(parts, clock)
	}
	if len(parts) == 0 {
		return "00:00:00"
	}
	return strings.Join(parts, " ")
}

func formatUUIDBytes(v []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
}

func redactToken(query, token string) string {
	if token == "" {
		return query
	}
	redacted := strings.ReplaceAll(query, sqlbuild.StringLiteral(token), "'<redacted>'")
	escapedToken := strings.ReplaceAll(token, "'", "''")
	redacted = strings.ReplaceAll(redacted, escapedToken, "<redacted>")
	return strings.ReplaceAll(redacted, token, "<redacted>")
}

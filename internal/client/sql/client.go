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
	db *sql.DB
	mu sync.Mutex
}

type Row struct {
	row    *sql.Row
	err    error
	unlock func()
}

// RowScanner is the narrow result contract consumed by provider resources and
// data sources. Keeping callers on this interface lets hermetic contract tests
// supply deterministic rows without replacing the production database/sql
// implementation.
type RowScanner interface {
	Scan(dest ...any) error
}

type contextConnector struct {
	*duckdb.Connector
	initialize func(context.Context, driver.ExecerContext) error
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

func (r *Row) Scan(dest ...any) error {
	if r.unlock != nil {
		defer r.unlock()
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
	if strings.TrimSpace(cfg.Token) == "" {
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
	connector := &contextConnector{Connector: duckdbConnector, initialize: func(ctx context.Context, execer driver.ExecerContext) error {
		initCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		queries := []string{
			"INSTALL motherduck",
			"LOAD motherduck",
			"SET motherduck_token = " + sqlbuild.StringLiteral(cfg.Token),
		}
		if cfg.AttachMode != "" {
			queries = append(queries, "SET motherduck_attach_mode = "+sqlbuild.StringLiteral(cfg.AttachMode))
		}
		if cfg.Database != "" {
			queries = append(queries, "ATTACH "+sqlbuild.StringLiteral("md:"+cfg.Database))
		}
		for _, query := range queries {
			tflog.Debug(initCtx, "running MotherDuck SQL boot query", map[string]any{"query": redactToken(query, cfg.Token)})
			if _, err := execer.ExecContext(initCtx, query, nil); err != nil {
				return fmt.Errorf("running boot query %q: %s", redactToken(query, cfg.Token), redactToken(err.Error(), cfg.Token))
			}
		}
		return nil
	}}

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Client{db: db}, nil
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

func (c *Client) Exec(ctx context.Context, query string, args ...any) error {
	if !c.Available() {
		return ErrMissingToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.execLocked(ctx, query, args...)
}

func (c *Client) execLocked(ctx context.Context, query string, args ...any) error {
	_, err := c.db.ExecContext(ctx, query, args...)
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
	c.mu.Lock()
	defer c.mu.Unlock()

	previous, err := c.scalarStringLocked(ctx, "SELECT current_database()")
	if err != nil || strings.TrimSpace(previous) == "" {
		previous = "memory"
	}
	if err := c.execLocked(ctx, "USE "+sqlbuild.QuoteIdentifier(database)); err != nil {
		return err
	}
	runErr := fn(func(query string, args ...any) error {
		return c.execLocked(ctx, query, args...)
	})
	restoreErr := c.execLocked(ctx, "USE "+sqlbuild.QuoteIdentifier(previous))
	if restoreErr != nil {
		return errors.Join(runErr, fmt.Errorf("restore previous database %q: %w", previous, restoreErr))
	}
	return runErr
}

func (c *Client) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if !c.Available() {
		return &Row{err: ErrMissingToken}
	}
	c.mu.Lock()
	return &Row{
		row: c.db.QueryRowContext(ctx, query, args...),
		unlock: func() {
			c.mu.Unlock()
		},
	}
}

func (c *Client) QueryRowsJSON(ctx context.Context, query string, args ...any) (result string, err error) {
	if !c.Available() {
		return "", ErrMissingToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
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
	for rows.Next() {
		values := make([]any, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			databaseTypeName := ""
			if i < len(columnTypes) {
				databaseTypeName = columnTypes[i].DatabaseTypeName()
			}
			row[strings.ToLower(col)] = normalizeValue(values[i], databaseTypeName)
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scalarStringLocked(ctx, query, args...)
}

func (c *Client) scalarStringLocked(ctx context.Context, query string, args ...any) (string, error) {
	var value sql.NullString
	if err := c.db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		return "", err
	}
	return value.String, nil
}

func (c *Client) Exists(ctx context.Context, query string, args ...any) (bool, error) {
	if !c.Available() {
		return false, ErrMissingToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var count int
	if err := c.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func normalizeValue(value any, databaseTypeName string) any {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		if strings.EqualFold(databaseTypeName, "UUID") && len(v) == 16 {
			return formatUUIDBytes(v)
		}
		return hex.EncodeToString(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	default:
		if _, err := json.Marshal(v); err == nil {
			return v
		}
		return fmt.Sprint(v)
	}
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

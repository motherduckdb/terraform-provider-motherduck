package providerctx

import (
	"context"
	"strings"
	"sync"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// SQLClient is the provider's complete SQL dependency. It is intentionally
// internal and contains only operations exercised by resources and data
// sources, so contract tests can run the real Terraform lifecycle against a
// strict in-memory fake.
type SQLClient interface {
	Available() bool
	AttachDatabase(context.Context, string) error
	Close() error
	Exec(context.Context, string, ...any) error
	Exists(context.Context, string, ...any) (bool, error)
	QueryRow(context.Context, string, ...any) mdsql.RowScanner
	QueryRowsJSON(context.Context, string, ...any) (string, error)
	ScalarString(context.Context, string, ...any) (string, error)
	WithDatabaseUse(context.Context, string, func(func(string, ...any) error) error) error
}

// RESTClient is the provider's complete REST dependency.
type RESTClient interface {
	Available() bool
	ActiveAccounts(context.Context) (*mdrest.ActiveAccountsResponse, error)
	CreateDiveEmbedSession(context.Context, string, mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error)
	CreateServiceAccount(context.Context, string) (*mdrest.ServiceAccount, error)
	CreateToken(context.Context, string, mdrest.CreateTokenRequest) (*mdrest.Token, error)
	DeleteToken(context.Context, string, string) error
	DeleteUser(context.Context, string) error
	GetDucklingConfig(context.Context, string) (*mdrest.DucklingConfig, error)
	ListTokens(context.Context, string) ([]mdrest.Token, error)
	SetDucklingConfig(context.Context, string, mdrest.DucklingConfig) (*mdrest.DucklingConfig, error)
}

type SQLClientFactory func(context.Context, mdsql.Config) (SQLClient, error)

type Context struct {
	SQL          SQLClient
	REST         RESTClient
	SQLConfig    mdsql.Config
	NewSQLClient SQLClientFactory

	sqlMu sync.Mutex
}

func (c *Context) SQLClient(ctx context.Context) (SQLClient, error) {
	if c == nil {
		return nil, mdsql.ErrMissingToken
	}
	c.sqlMu.Lock()
	defer c.sqlMu.Unlock()
	if c.SQL != nil {
		return c.SQL, nil
	}
	if strings.TrimSpace(c.SQLConfig.Token) == "" {
		return nil, mdsql.ErrMissingToken
	}
	factory := c.NewSQLClient
	if factory == nil {
		factory = func(ctx context.Context, cfg mdsql.Config) (SQLClient, error) {
			return mdsql.New(ctx, cfg)
		}
	}
	client, err := factory(ctx, c.SQLConfig)
	if err != nil {
		return nil, err
	}
	c.SQL = client
	return c.SQL, nil
}

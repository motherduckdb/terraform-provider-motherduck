package providerctx

import (
	"context"
	"strings"
	"sync"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

type Context struct {
	SQL       *mdsql.Client
	REST      *mdrest.Client
	SQLConfig mdsql.Config

	sqlMu sync.Mutex
}

func (c *Context) SQLClient(ctx context.Context) (*mdsql.Client, error) {
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
	client, err := mdsql.New(ctx, c.SQLConfig)
	if err != nil {
		return nil, err
	}
	c.SQL = client
	return c.SQL, nil
}

package sqlcatalog

import (
	"context"
	stdsql "database/sql"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

type OwnedShare struct {
	URL            stdsql.NullString
	SourceDatabase stdsql.NullString
	Access         stdsql.NullString
	Visibility     stdsql.NullString
	UpdateMode     stdsql.NullString
	IncludePattern stdsql.NullString
	CreatedTS      stdsql.NullString
}

type ownedShareClient interface {
	QueryRow(context.Context, string, ...any) *mdsql.Row
}

func ReadOwnedShare(ctx context.Context, client ownedShareClient, name string) (OwnedShare, error) {
	var share OwnedShare
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, `SELECT url, source_db_name, access, visibility, update, to_json(include_pattern)::VARCHAR, created_ts::VARCHAR FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name = ?`, name).Scan(
			&share.URL,
			&share.SourceDatabase,
			&share.Access,
			&share.Visibility,
			&share.UpdateMode,
			&share.IncludePattern,
			&share.CreatedTS,
		)
	})
	return share, err
}

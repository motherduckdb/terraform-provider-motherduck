package sqlcatalog

import (
	"strings"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// SecretByName returns a query and its arguments that select the given
// columns of the MotherDuck secret a configured name resolves to.
//
// DuckDB stores a secret name with only the ASCII letters A to Z lowered, so
// `MySecret` is stored as `mysecret`, while `Äpfel` and `äpfel` are two
// different secrets. lower() also folds non-ASCII letters, so the filter
// fetches every candidate and the ordering prefers the stored spelling of the
// configured name. A name that only lower() matches is still found.
func SecretByName(columns, name string) (string, []any) {
	query := "SELECT " + columns + " FROM duckdb_secrets() WHERE lower(name) = lower(?) AND storage = 'motherduck' ORDER BY name = ? DESC, name LIMIT 1"
	return query, []any{name, FoldSecretName(name)}
}

// SecretNameFilter returns a condition on duckdb_secrets() rows that keeps the
// MotherDuck secrets a configured name resolves to, by the same rule as
// SecretByName. When a secret has the stored spelling of the name, only that
// secret matches. Otherwise every secret that lower() matches is kept.
func SecretNameFilter(name string) string {
	stored := sqlbuild.StringLiteral(FoldSecretName(name))
	return "lower(name) = lower(" + sqlbuild.StringLiteral(name) + ") AND (name = " + stored +
		" OR NOT EXISTS (SELECT 1 FROM duckdb_secrets() AS stored WHERE stored.storage = 'motherduck' AND stored.name = " + stored + "))"
}

// FoldSecretName returns the spelling DuckDB stores for a secret name.
func FoldSecretName(name string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, name)
}

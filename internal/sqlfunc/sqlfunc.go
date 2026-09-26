// Package sqlfunc probes which MotherDuck SQL functions the current session
// exposes. Resources and data sources share it so every surface uses the same
// probe query and the same per-operation cache.
package sqlfunc

import (
	"context"
	"strings"
	"sync"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

// Exister is the SQL client dependency needed to probe for a function.
type Exister interface {
	Exists(context.Context, string, ...any) (bool, error)
}

const probeQuery = "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = lower(?)"

type cacheKey struct{}

type cache struct {
	mu        sync.Mutex
	available map[string]bool
}

// WithCache returns a context that remembers probe results. Install it at the
// start of one Terraform operation so repeated checks for the same function,
// for example before a write and again in the read-back, cost one round trip.
// Function availability does not change within a single operation.
func WithCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(cacheKey{}).(*cache); ok {
		return ctx
	}
	return context.WithValue(ctx, cacheKey{}, &cache{available: map[string]bool{}})
}

// Exists reports whether the current SQL session exposes the named function.
// Probe errors are returned and never cached.
func Exists(ctx context.Context, client Exister, name string) (bool, error) {
	key := strings.ToLower(name)
	c, _ := ctx.Value(cacheKey{}).(*cache)
	if c != nil {
		c.mu.Lock()
		available, ok := c.available[key]
		c.mu.Unlock()
		if ok {
			return available, nil
		}
	}
	var available bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		available, existsErr = client.Exists(ctx, probeQuery, name)
		return existsErr
	})
	if err != nil {
		return false, err
	}
	if c != nil {
		c.mu.Lock()
		c.available[key] = available
		c.mu.Unlock()
	}
	return available, nil
}

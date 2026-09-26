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
	return probe(ctx, client, strings.ToLower(name), probeQuery, name)
}

const parameterProbeQuery = "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = lower(?) AND list_contains(list_transform(parameters, lambda p: lower(p)), lower(?))"

// ParameterExists reports whether the named function accepts the named
// parameter in the current SQL session. duckdb_functions() lists the named
// parameters of table functions, so a client that added an optional argument
// can be told apart from one that did not. Probe errors are returned and
// never cached.
func ParameterExists(ctx context.Context, client Exister, function, parameter string) (bool, error) {
	key := strings.ToLower(function) + "(" + strings.ToLower(parameter) + ")"
	return probe(ctx, client, key, parameterProbeQuery, function, parameter)
}

func probe(ctx context.Context, client Exister, key, query string, args ...any) (bool, error) {
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
		available, existsErr = client.Exists(ctx, query, args...)
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

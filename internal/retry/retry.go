package retry

import (
	"context"
	stdsql "database/sql"
	"errors"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

const (
	sqlMaxAttempts = 4
	sqlRetryDelay  = 2 * time.Second
)

// SQL runs an operation with the retry policy used for MotherDuck SQL calls.
func SQL(ctx context.Context, operation func() error) error {
	return sql(ctx, sqlRetryDelay, operation)
}

func sql(ctx context.Context, delay time.Duration, operation func() error) error {
	var err error
	for attempt := 0; attempt < sqlMaxAttempts; attempt++ {
		err = operation()
		if err == nil || err == stdsql.ErrNoRows || isCatalogNotFound(err) || !isTransientMotherDuckError(err) || attempt == sqlMaxAttempts-1 {
			return err
		}
		if err := Sleep(ctx, delay); err != nil {
			return err
		}
	}
	return err
}

// Sleep waits for a delay or returns early when the context is canceled.
func Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isCatalogNotFound(err error) bool {
	var duckErr *duckdb.Error
	if !errors.As(err, &duckErr) || duckErr.Type != duckdb.ErrorTypeCatalog {
		return false
	}
	message := strings.ToLower(duckErr.Error())
	return strings.Contains(message, "does not exist") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "no database/share named")
}

func isTransientMotherDuckError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "deadline_exceeded") ||
		strings.Contains(message, "unavailable") ||
		strings.Contains(message, "request timed out") ||
		strings.Contains(message, "failed to connect to all addresses") ||
		strings.Contains(message, "network is unreachable")
}

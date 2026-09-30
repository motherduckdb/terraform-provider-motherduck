package datasources

import (
	"context"
	"errors"
	"testing"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

type admissionSQLClient struct {
	providerctx.SQLClient
	available      bool
	availableCalls int
}

func (c *admissionSQLClient) Available() bool {
	c.availableCalls++
	return c.available
}

func TestBaseDataSourceSQLAdmission(t *testing.T) {
	const summary = "MotherDuck token required"

	t.Run("nil context", func(t *testing.T) {
		assertDataSourceSQL(t, context.Background(), nil, nil, nil, summary, mdsql.ErrMissingToken.Error(), 0, false, false)
	})
	t.Run("blank token", func(t *testing.T) {
		assertDataSourceSQL(t, context.Background(), &providerctx.Context{}, nil, nil, summary, mdsql.ErrMissingToken.Error(), 0, false, false)
	})
	t.Run("cached unavailable stays unavailable", func(t *testing.T) {
		client := &admissionSQLClient{}
		assertDataSourceSQL(t, context.Background(), &providerctx.Context{SQL: client}, client, nil, summary, mdsql.ErrMissingToken.Error(), 2, true, true)
	})
	t.Run("unavailable factory result stays cached", func(t *testing.T) {
		client := &admissionSQLClient{}
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				return client, nil
			},
		}
		assertDataSourceSQL(t, context.Background(), provider, client, nil, summary, mdsql.ErrMissingToken.Error(), 2, true, true)
		if factoryCalls != 1 || provider.SQL != client {
			t.Fatalf("factory calls = %d, cached client = %v, want one call and the unavailable client", factoryCalls, provider.SQL)
		}
	})
	t.Run("nil factory result is retried", func(t *testing.T) {
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				return nil, nil
			},
		}
		assertDataSourceSQL(t, context.Background(), provider, nil, nil, summary, mdsql.ErrMissingToken.Error(), 0, true, true)
		if factoryCalls != 2 {
			t.Fatalf("factory calls = %d, want 2", factoryCalls)
		}
	})
	t.Run("factory error takes precedence over client", func(t *testing.T) {
		client := &admissionSQLClient{available: true}
		factoryErr := errors.New("factory failed")
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				return client, factoryErr
			},
		}
		assertDataSourceSQL(t, context.Background(), provider, client, nil, summary, factoryErr.Error(), 0, true, true)
		if factoryCalls != 2 || provider.SQL != nil {
			t.Fatalf("factory calls = %d, cached client = %v, want two calls and no cached client", factoryCalls, provider.SQL)
		}
	})
	t.Run("available cache", func(t *testing.T) {
		client := &admissionSQLClient{available: true}
		assertDataSourceSQL(t, context.Background(), &providerctx.Context{SQL: client}, client, client, "", "", 2, true, false)
	})
	t.Run("successful factory reuse", func(t *testing.T) {
		client := &admissionSQLClient{available: true}
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				return client, nil
			},
		}
		assertDataSourceSQL(t, context.Background(), provider, client, client, "", "", 2, true, false)
		if factoryCalls != 1 {
			t.Fatalf("factory calls = %d, want 1", factoryCalls)
		}
	})
	t.Run("failed factory then recovery", func(t *testing.T) {
		client := &admissionSQLClient{available: true}
		factoryErr := errors.New("factory failed")
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				if factoryCalls == 1 {
					return nil, factoryErr
				}
				return client, nil
			},
		}
		assertDataSourceSQL(t, context.Background(), provider, client, client, summary, factoryErr.Error(), 1, true, false)
		if factoryCalls != 2 {
			t.Fatalf("factory calls = %d, want 2", factoryCalls)
		}
	})
	t.Run("canceled factory then recovery", func(t *testing.T) {
		client := &admissionSQLClient{available: true}
		factoryCalls := 0
		provider := &providerctx.Context{
			SQLConfig: mdsql.Config{Token: "test-token"},
			NewSQLClient: func(ctx context.Context, _ mdsql.Config) (providerctx.SQLClient, error) {
				factoryCalls++
				if factoryCalls == 1 {
					return nil, ctx.Err()
				}
				return client, nil
			},
		}
		assertDataSourceSQL(t, canceledDataSourceContext(t), provider, client, client, summary, context.Canceled.Error(), 1, true, false)
		if factoryCalls != 2 {
			t.Fatalf("factory calls = %d, want 2", factoryCalls)
		}
	})
}

func assertDataSourceSQL(t *testing.T, ctx context.Context, provider *providerctx.Context, observed, wantClient providerctx.SQLClient, wantSummary, wantDetail string, wantAvailableCalls int, invokeTwice, secondError bool) {
	t.Helper()
	datasource := &baseDataSource{provider: provider}
	var firstDiags diag.Diagnostics
	firstDiags.AddWarning("existing warning", "preserve this diagnostic")
	first := datasource.sql(ctx, &firstDiags)
	if wantSummary == "" {
		if first != wantClient || firstDiags.HasError() || len(firstDiags) != 1 {
			t.Fatalf("first SQL result = (%v, %v), want client %v without diagnostics", first, firstDiags, wantClient)
		}
	} else {
		assertDataSourceAdmissionDiagnostic(t, firstDiags, wantSummary, wantDetail)
		if first != nil {
			t.Fatalf("first SQL client = %v, want nil", first)
		}
	}
	if invokeTwice {
		var secondDiags diag.Diagnostics
		secondDiags.AddWarning("existing warning", "preserve this diagnostic")
		second := datasource.sql(context.Background(), &secondDiags)
		if !secondError {
			if second != wantClient || secondDiags.HasError() || len(secondDiags) != 1 {
				t.Fatalf("second SQL result = (%v, %v), want client %v without diagnostics", second, secondDiags, wantClient)
			}
		} else {
			assertDataSourceAdmissionDiagnostic(t, secondDiags, wantSummary, wantDetail)
			if second != nil {
				t.Fatalf("second SQL client = %v, want nil", second)
			}
		}
	}
	if provider != nil && wantClient != nil && provider.SQL != wantClient {
		t.Fatalf("cached SQL client = %v, want %v", provider.SQL, wantClient)
	}
	if observed != nil {
		client, ok := observed.(*admissionSQLClient)
		if !ok {
			t.Fatalf("observed client type = %T, want *admissionSQLClient", observed)
		}
		if client.availableCalls != wantAvailableCalls {
			t.Fatalf("available calls = %d, want %d", client.availableCalls, wantAvailableCalls)
		}
	}
}

func assertDataSourceAdmissionDiagnostic(t *testing.T, diags diag.Diagnostics, wantSummary, wantDetail string) {
	t.Helper()
	if len(diags) != 2 || !diags.HasError() {
		t.Fatalf("diagnostics = %v, want existing warning followed by one error", diags)
	}
	if diags[0].Severity() != diag.SeverityWarning || diags[0].Summary() != "existing warning" || diags[0].Detail() != "preserve this diagnostic" {
		t.Fatalf("existing diagnostic = (%q, %q), want warning preserved", diags[0].Summary(), diags[0].Detail())
	}
	if diags[1].Severity() != diag.SeverityError || diags[1].Summary() != wantSummary || diags[1].Detail() != wantDetail {
		t.Fatalf("diagnostic = (%q, %q), want (%q, %q)", diags[1].Summary(), diags[1].Detail(), wantSummary, wantDetail)
	}
}

func canceledDataSourceContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

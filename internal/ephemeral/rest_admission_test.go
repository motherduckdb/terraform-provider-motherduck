package ephemeral

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestDiveEmbedSessionRESTAdmission(t *testing.T) {
	httpClient := &http.Client{Transport: admissionRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("REST admission made an HTTP request")
		return nil, errors.New("unexpected HTTP request")
	})}
	available, err := mdrest.New("https://127.0.0.1", "admin-token", mdrest.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	blank, err := mdrest.New("https://127.0.0.1", " \t\n ")
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		provider *providerctx.Context
		want     *mdrest.Client
		wantErr  bool
	}{
		"nil provider":     {wantErr: true},
		"nil REST":         {provider: &providerctx.Context{}, wantErr: true},
		"zero value REST":  {provider: &providerctx.Context{REST: &mdrest.Client{}}, wantErr: true},
		"blank token REST": {provider: &providerctx.Context{REST: blank}, wantErr: true},
		"available REST":   {provider: &providerctx.Context{REST: available}, want: available},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			factoryCalled := false
			if tc.provider != nil {
				tc.provider.SQLConfig.Token = "sql-token"
				tc.provider.NewSQLClient = func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
					factoryCalled = true
					return nil, errors.New("SQL factory must not run")
				}
			}
			resource := &diveEmbedSessionEphemeralResource{provider: tc.provider}
			var diags diag.Diagnostics
			diags.AddWarning("existing warning", "preserve this diagnostic")
			diags.AddError("existing error", "preserve this error")
			got := resource.rest(&diags)

			if got != tc.want {
				t.Fatalf("REST client = %p, want %p", got, tc.want)
			}
			if factoryCalled {
				t.Fatal("REST admission initialized SQL")
			}
			if tc.wantErr {
				if len(diags) != 3 || !diags.HasError() {
					t.Fatalf("diagnostics = %v, want existing diagnostics followed by one error", diags)
				}
				if diags[0].Severity() != diag.SeverityWarning || diags[0].Summary() != "existing warning" || diags[0].Detail() != "preserve this diagnostic" {
					t.Fatalf("existing diagnostic = %v, want preserved warning first", diags[0])
				}
				if diags[1].Severity() != diag.SeverityError || diags[1].Summary() != "existing error" || diags[1].Detail() != "preserve this error" {
					t.Fatalf("existing diagnostic = %v, want preserved error second", diags[1])
				}
				if diags[2].Severity() != diag.SeverityError || diags[2].Summary() != "MotherDuck admin token required" || diags[2].Detail() != mdrest.ErrMissingAdminToken.Error() {
					t.Fatalf("REST diagnostic = %v, want missing admin token error", diags[2])
				}
				return
			}
			if len(diags) != 2 || diags[0].Summary() != "existing warning" || diags[1].Summary() != "existing error" {
				t.Fatalf("diagnostics = %v, want existing diagnostics preserved", diags)
			}
		})
	}
}

type admissionRoundTripper func(*http.Request) (*http.Response, error)

func (f admissionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

package providerctx

import (
	"errors"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestAvailableRESTClient(t *testing.T) {
	available, err := mdrest.New("https://127.0.0.1", "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	blank, err := mdrest.New("https://127.0.0.1", " \t\n ")
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		provider *Context
		want     *mdrest.Client
		wantErr  bool
	}{
		"nil receiver":     {wantErr: true},
		"nil REST":         {provider: &Context{}, wantErr: true},
		"zero value REST":  {provider: &Context{REST: &mdrest.Client{}}, wantErr: true},
		"blank token REST": {provider: &Context{REST: blank}, wantErr: true},
		"available REST":   {provider: &Context{REST: available}, want: available},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tc.provider.AvailableRESTClient()
			if got != tc.want {
				t.Fatalf("REST client = %p, want %p", got, tc.want)
			}
			if tc.wantErr {
				if !errors.Is(err, mdrest.ErrMissingAdminToken) {
					t.Fatalf("error = %v, want errors.Is ErrMissingAdminToken", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

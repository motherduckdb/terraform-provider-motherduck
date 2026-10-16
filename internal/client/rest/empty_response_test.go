package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientRejectsMissingResponseObjects(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "whitespace": " \n\t", "null": " \nnull\t"} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			calls := []func() error{
				func() error { _, err := client.ListTokens(t.Context(), "svc"); return err },
				func() error {
					_, err := client.CreateToken(t.Context(), "svc", CreateTokenRequest{Name: "test"})
					return err
				},
				func() error { _, err := client.GetDucklingConfig(t.Context(), "svc"); return err },
			}
			for i, call := range calls {
				if err := call(); err == nil || !strings.Contains(err.Error(), "expected a JSON response object") {
					t.Errorf("call %d returned %v, want missing response object error", i, err)
				}
			}
			if got := int(requests.Load()); got != len(calls) {
				t.Errorf("requests = %d, want %d without replay", got, len(calls))
			}
		})
	}
}

func TestClientAcceptsExplicitEmptyInventoryAndNoContentDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"tokens":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.ListTokens(t.Context(), "svc")
	if err != nil || len(tokens) != 0 {
		t.Fatalf("explicit empty inventory = %v, %v", tokens, err)
	}
	if err := client.DeleteToken(t.Context(), "svc", "token-id"); err != nil {
		t.Fatal(err)
	}
}

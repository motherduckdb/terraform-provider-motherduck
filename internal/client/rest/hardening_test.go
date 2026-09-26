package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateBaseURL(t *testing.T) {
	tests := map[string]struct {
		value   string
		wantErr string
	}{
		"https":            {value: "https://api.motherduck.com"},
		"https prefix":     {value: "https://api.motherduck.com/prefix"},
		"http localhost":   {value: "http://localhost:8080/prefix"},
		"http loopback v4": {value: "http://127.0.0.1:9000"},
		"http loopback v6": {value: "http://[::1]:9000"},
		"http remote":      {value: "http://api.motherduck.com", wantErr: "must use https"},
		"missing scheme":   {value: "api.motherduck.com", wantErr: "absolute HTTP or HTTPS URL"},
		"missing host":     {value: "https:///api", wantErr: "absolute HTTP or HTTPS URL"},
		"bad scheme":       {value: "ftp://api.motherduck.com", wantErr: "http or https scheme"},
		"credentials":      {value: "https://user:pass@api.motherduck.com", wantErr: "username or password credentials"},
		"query":            {value: "https://h/?x=1", wantErr: "query string or fragment"},
		"empty query":      {value: "https://h/?", wantErr: "query string or fragment"},
		"fragment":         {value: "https://h/#v1", wantErr: "query string or fragment"},
		"leading space":    {value: " https://api.motherduck.com", wantErr: "whitespace"},
		"trailing newline": {value: "https://api.motherduck.com\n", wantErr: "whitespace"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidateBaseURL(tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateBaseURL(%q) = %v", tc.value, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateBaseURL(%q) = %v, want error containing %q", tc.value, err, tc.wantErr)
			}
			if _, newErr := New(tc.value, "token"); newErr == nil {
				t.Fatalf("New(%q) accepted an invalid base URL", tc.value)
			}
		})
	}
}

func TestRequestPathRejectsDotAndEmptySegments(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]error{
		"dot dot username": client.DeleteUser(t.Context(), ".."),
		"dot token":        client.DeleteToken(t.Context(), "svc", "."),
		"empty username":   client.DeleteToken(t.Context(), "", "token-id"),
	}
	for name, err := range calls {
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
	if err := client.DeleteToken(t.Context(), "svc", "..token"); err != nil {
		t.Fatalf("identifier that only starts with dots was rejected: %v", err)
	}
}

func TestWithTimeoutDoesNotDependOnOptionOrderOrMutateCaller(t *testing.T) {
	caller := &http.Client{Timeout: 5 * time.Second}
	for name, opts := range map[string][]Option{
		"timeout first": {WithTimeout(7 * time.Second), WithHTTPClient(caller)},
		"timeout last":  {WithHTTPClient(caller), WithTimeout(7 * time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := New("https://api.motherduck.com", "token", opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := client.httpClient.Timeout; got != 7*time.Second {
				t.Fatalf("timeout = %s, want 7s", got)
			}
			if client.httpClient == caller {
				t.Fatal("client reused the caller's http.Client")
			}
			if caller.Timeout != 5*time.Second {
				t.Fatalf("caller timeout changed to %s", caller.Timeout)
			}
		})
	}
}

func TestPOSTRetriesOnlyTooManyRequests(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"svc"}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	account, err := client.CreateServiceAccount(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if account.Username != "svc" {
		t.Fatalf("username = %q", account.Username)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestRetryableStatusByMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPost} {
		if !retryableStatus(method, http.StatusTooManyRequests) {
			t.Errorf("%s 429 should retry", method)
		}
	}
	if retryableStatus(http.MethodPost, http.StatusServiceUnavailable) {
		t.Error("POST 503 must not retry")
	}
	if !retryableStatus(http.MethodGet, http.StatusServiceUnavailable) {
		t.Error("GET 503 should retry")
	}
}

func TestJitterStaysInUpperHalf(t *testing.T) {
	base := retryDelay(3)
	for range 200 {
		got := jitter(base)
		if got < base/2 || got > base {
			t.Fatalf("jitter(%s) = %s, want within [%s, %s]", base, got, base/2, base)
		}
	}
}

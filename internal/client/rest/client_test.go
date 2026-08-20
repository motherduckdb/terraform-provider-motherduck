package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCreateToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer admin-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/v1/users/svc/tokens"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(Token{
			Token:     "md_secret",
			ID:        "token-id",
			CreatedTS: "2026-06-19T00:00:00Z",
			TokenType: "read_write",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	token, err := client.CreateToken(context.Background(), "svc", CreateTokenRequest{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if token.ID != "token-id" || token.Token != "md_secret" {
		t.Fatalf("unexpected token response: %#v", token)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "FORBIDDEN",
			"message": "Unauthorized",
			"issues":  []map[string]string{{"message": "minimum role org_admin is required"}},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ActiveAccounts(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "FORBIDDEN" {
		t.Fatalf("unexpected API error: %#v", apiErr)
	}
	if got := apiErr.Error(); !strings.Contains(got, "Unauthorized") || !strings.Contains(got, "minimum role org_admin is required") {
		t.Fatalf("error string dropped API issue details: %q", got)
	}
}

func TestClientCoversPublicPaths(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer admin-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if got, want := r.Header.Get("Content-Type"), "application/json"; got != want {
				t.Fatalf("Content-Type = %q, want %q", got, want)
			}
		}
		call := r.Method + " " + r.URL.EscapedPath()
		calls = append(calls, call)
		switch call {
		case "POST /v1/users":
			_ = json.NewEncoder(w).Encode(ServiceAccount{Username: "svc"})
		case "GET /v1/users/svc/tokens":
			_ = json.NewEncoder(w).Encode(ListTokensResponse{Tokens: []Token{{ID: "tok"}}})
		case "POST /v1/users/svc/tokens":
			_ = json.NewEncoder(w).Encode(Token{ID: "tok", Token: "md_secret"})
		case "DELETE /v1/users/svc/tokens/tok":
			w.WriteHeader(http.StatusNoContent)
		case "GET /v1/users/svc/instances", "PUT /v1/users/svc/instances":
			_ = json.NewEncoder(w).Encode(DucklingConfig{
				ReadWrite:   DucklingReadWriteConfig{InstanceSize: "standard"},
				ReadScaling: DucklingReadScalingConfig{InstanceSize: "standard", FlockSize: 1},
			})
		case "GET /v1/active_accounts":
			_ = json.NewEncoder(w).Encode(ActiveAccountsResponse{Accounts: []ActiveAccount{{Username: "svc"}}})
		case "POST /v1/dives/dive-id/embed-session":
			_ = json.NewEncoder(w).Encode(EmbedSessionResponse{Session: "session"})
		case "DELETE /v1/users/svc":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s", call)
		}
	}))
	defer server.Close()

	client, err := New(server.URL+"/", "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mustNoErr(t, client.DeleteUser(ctx, "svc"))
	if _, err := client.CreateServiceAccount(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTokens(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateToken(ctx, "svc", CreateTokenRequest{Name: "token"}); err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, client.DeleteToken(ctx, "svc", "tok"))
	if _, err := client.GetDucklingConfig(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetDucklingConfig(ctx, "svc", DucklingConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ActiveAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateDiveEmbedSession(ctx, "dive-id", EmbedSessionRequest{Username: "svc"}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"DELETE /v1/users/svc",
		"POST /v1/users",
		"GET /v1/users/svc/tokens",
		"POST /v1/users/svc/tokens",
		"DELETE /v1/users/svc/tokens/tok",
		"GET /v1/users/svc/instances",
		"PUT /v1/users/svc/instances",
		"GET /v1/active_accounts",
		"POST /v1/dives/dive-id/embed-session",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestClientRetriesIdempotentRequestsAndSendsUserAgent(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if got, want := r.Header.Get("User-Agent"), "terraform-provider-motherduck/test"; got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(ListTokensResponse{Tokens: []Token{{ID: "tok"}}})
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token", WithUserAgent("terraform-provider-motherduck/test"))
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.ListTokens(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(tokens) != 1 || tokens[0].ID != "tok" {
		t.Fatalf("tokens = %#v", tokens)
	}
}

func TestClientAcceptsSuccessfulResponsesLargerThanErrorBodyLimit(t *testing.T) {
	username := strings.Repeat("a", maxErrorBodyBytes+1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ActiveAccountsResponse{Accounts: []ActiveAccount{{Username: username}}})
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := client.ActiveAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.Accounts) != 1 || accounts.Accounts[0].Username != username {
		t.Fatal("large successful response was not decoded intact")
	}
}

func TestRetryAfterDelayIsCapped(t *testing.T) {
	if got := retryAfterDelay("3600"); got != maxRetryAfterDelay {
		t.Fatalf("retryAfterDelay() = %s, want %s", got, maxRetryAfterDelay)
	}
}

func TestClientPaginatesTokenAndActiveAccountLists(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.URL.RequestURI() {
		case "/v1/users/svc/tokens":
			_ = json.NewEncoder(w).Encode(ListTokensResponse{
				Tokens:     []Token{{ID: "tok-1"}},
				NextCursor: "page-2",
			})
		case "/v1/users/svc/tokens?cursor=page-2":
			_ = json.NewEncoder(w).Encode(ListTokensResponse{
				Tokens: []Token{{ID: "tok-2"}},
			})
		case "/v1/active_accounts":
			_ = json.NewEncoder(w).Encode(ActiveAccountsResponse{
				Accounts:   []ActiveAccount{{Username: "svc-1"}},
				Pagination: pageInfo{NextCursor: "page-2"},
			})
		case "/v1/active_accounts?cursor=page-2":
			_ = json.NewEncoder(w).Encode(ActiveAccountsResponse{
				Accounts: []ActiveAccount{{Username: "svc-2"}},
			})
		default:
			t.Fatalf("unexpected request %s", r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.ListTokens(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{tokens[0].ID, tokens[1].ID}, []string{"tok-1", "tok-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("token ids = %#v, want %#v", got, want)
	}
	accounts, err := client.ActiveAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{accounts.Accounts[0].Username, accounts.Accounts[1].Username}, []string{"svc-1", "svc-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("account usernames = %#v, want %#v", got, want)
	}

	wantCalls := []string{
		"GET /v1/users/svc/tokens",
		"GET /v1/users/svc/tokens?cursor=page-2",
		"GET /v1/active_accounts",
		"GET /v1/active_accounts?cursor=page-2",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestClientDetectsPaginationLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/svc/tokens":
			_ = json.NewEncoder(w).Encode(ListTokensResponse{
				Tokens:     []Token{{ID: "tok"}},
				NextCursor: "same-page",
			})
		case "/v1/active_accounts":
			_ = json.NewEncoder(w).Encode(ActiveAccountsResponse{
				Accounts:   []ActiveAccount{{Username: "svc"}},
				NextCursor: "same-page",
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func() error{
		"tokens": func() error {
			_, err := client.ListTokens(context.Background(), "svc")
			return err
		},
		"active accounts": func() error {
			_, err := client.ActiveAccounts(context.Background())
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "pagination loop") {
				t.Fatalf("error = %v, want pagination loop", err)
			}
		})
	}
}

func TestMissingAdminToken(t *testing.T) {
	client, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if client.Available() {
		t.Fatal("empty admin token should not be available")
	}
	if _, err := client.ActiveAccounts(context.Background()); err != ErrMissingAdminToken {
		t.Fatalf("error = %v, want ErrMissingAdminToken", err)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

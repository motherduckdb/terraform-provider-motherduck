package rest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestClientPaginationCursorPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		fields string
		cursor string
	}{
		{"absent", ``, ""},
		{"null", `,"next_cursor":null,"next_page_token":null,"pagination":null`, ""},
		{"empty", `,"next_cursor":"","next_page_token":"","pagination":{"next_cursor":"","next_page_token":"","cursor":""}`, ""},
		{"top cursor wins", `,"next_cursor":"top-cursor","next_page_token":"top-token","pagination":{"next_cursor":"nested-cursor","next_page_token":"nested-token","cursor":"nested-fallback"}`, "top-cursor"},
		{"top token wins", `,"next_cursor":"","next_page_token":"top-token","pagination":{"next_cursor":"nested-cursor","next_page_token":"nested-token","cursor":"nested-fallback"}`, "top-token"},
		{"nested cursor wins", `,"next_cursor":null,"pagination":{"next_cursor":"nested-cursor","next_page_token":"nested-token","cursor":"nested-fallback"}`, "nested-cursor"},
		{"nested token wins", `,"pagination":{"next_cursor":"","next_page_token":"nested-token","cursor":"nested-fallback"}`, "nested-token"},
		{"nested fallback", `,"pagination":{"next_cursor":null,"next_page_token":"","cursor":"nested-fallback"}`, "nested-fallback"},
		{"opaque cursor", `,"next_cursor":"page+2/%& ?=é"`, "page+2/%& ?=é"},
		{"whitespace cursor skipped", `,"next_cursor":" \t","next_page_token":"top-token"`, "top-token"},
		{"padded cursor preserved", `,"next_cursor":" page-2 "`, " page-2 "},
	}
	endpoints := []struct {
		name    string
		path    string
		items   string
		readIDs func(*Client) ([]string, error)
	}{
		{
			name: "tokens", path: "/v1/users/svc/tokens", items: `"tokens":[{"id":%q}]`,
			readIDs: func(client *Client) ([]string, error) {
				tokens, err := client.ListTokens(t.Context(), "svc")
				var ids []string
				for _, token := range tokens {
					ids = append(ids, token.ID)
				}
				return ids, err
			},
		},
		{
			name: "accounts", path: "/v1/active_accounts", items: `"accounts":[{"username":%q}]`,
			readIDs: func(client *Client) ([]string, error) {
				accounts, err := client.ActiveAccounts(t.Context())
				if err != nil {
					return nil, err
				}
				var ids []string
				for _, account := range accounts.Accounts {
					ids = append(ids, account.Username)
				}
				return ids, nil
			},
		},
	}
	for _, endpoint := range endpoints {
		for _, tc := range cases {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				requests := make(chan string, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- r.URL.RequestURI()
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method != http.MethodGet || r.URL.Path != endpoint.path:
						http.Error(w, "unexpected endpoint", http.StatusBadRequest)
					case r.URL.RawQuery == "":
						_, _ = fmt.Fprintf(w, "{%s%s}", fmt.Sprintf(endpoint.items, "first"), tc.fields)
					case tc.cursor != "" && r.URL.RawQuery == "page_token="+url.QueryEscape(tc.cursor):
						_, _ = fmt.Fprintf(w, "{%s}", fmt.Sprintf(endpoint.items, "second"))
					default:
						http.Error(w, "unexpected cursor", http.StatusBadRequest)
					}
				}))
				defer server.Close()
				client, err := New(server.URL, "admin-token")
				if err != nil {
					t.Fatal(err)
				}
				ids, err := endpoint.readIDs(client)
				if err != nil {
					t.Fatal(err)
				}
				wantIDs := []string{"first"}
				wantRequests := []string{endpoint.path}
				if tc.cursor != "" {
					wantIDs = append(wantIDs, "second")
					wantRequests = append(wantRequests, endpoint.path+"?page_token="+url.QueryEscape(tc.cursor))
				}
				if !reflect.DeepEqual(ids, wantIDs) {
					t.Fatalf("items = %v, want %v", ids, wantIDs)
				}
				var gotRequests []string
				for len(requests) > 0 {
					gotRequests = append(gotRequests, <-requests)
				}
				if !reflect.DeepEqual(gotRequests, wantRequests) {
					t.Fatalf("requests = %v, want %v", gotRequests, wantRequests)
				}
			})
		}
	}
}

func TestClientListUsersWalksPageTokensAndDropsRepeats(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != "/v1/users" {
			http.Error(w, "unexpected endpoint", http.StatusBadRequest)
			return
		}
		switch r.URL.Query().Get("page_token") {
		case "":
			_, _ = fmt.Fprint(w, `{"users":[{"id":"u1","username":"alice","roles":["admin"]},{"id":"u2","username":"bob","roles":[]}],"total_count":3,"next_page_token":"tok-2"}`)
		case "tok-2":
			// A user inserted mid-walk shifts bob onto the second page as well.
			_, _ = fmt.Fprint(w, `{"users":[{"id":"u2","username":"bob","roles":[]},{"id":"u3","username":"svc","is_service_account":true,"roles":[]}],"total_count":4,"next_page_token":null}`)
		default:
			http.Error(w, "unexpected page token", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	isDeprovisioned := false
	users, err := client.ListUsers(t.Context(), ListUsersFilter{IsDeprovisioned: &isDeprovisioned})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	if want := []string{"u1", "u2", "u3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("user ids = %v, want %v", ids, want)
	}
	if !users[2].IsServiceAccount || !reflect.DeepEqual(users[0].Roles, []string{"admin"}) {
		t.Fatalf("users = %#v, want decoded service account flag and roles", users)
	}
	wantRequests := []string{
		"/v1/users?is_deprovisioned=false&limit=1000",
		"/v1/users?is_deprovisioned=false&limit=1000&page_token=tok-2",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

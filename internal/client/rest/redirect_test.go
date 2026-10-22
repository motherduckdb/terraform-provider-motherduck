package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// redirectTarget records whether a redirected request reached it and which
// Authorization header it carried.
type redirectTarget struct {
	requests      atomic.Int32
	authorization atomic.Value
	server        *httptest.Server
}

func (r *redirectTarget) handler(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)
	r.authorization.Store(req.Header.Get("Authorization"))
	_, _ = w.Write([]byte(`{"tokens":[]}`))
}

// TestRedirectNeverSendsAdminTokenOverHTTPOrToAnotherHost follows the API to
// a redirect target. net/http compares only host names when it decides to
// keep the Authorization header, so before the redirect policy it sent the
// admin token over cleartext HTTP and to a different port on the same host.
func TestRedirectNeverSendsAdminTokenOverHTTPOrToAnotherHost(t *testing.T) {
	plain := &redirectTarget{}
	plain.server = httptest.NewServer(http.HandlerFunc(plain.handler))
	defer plain.server.Close()
	otherPort := &redirectTarget{}
	otherPort.server = httptest.NewTLSServer(http.HandlerFunc(otherPort.handler))
	defer otherPort.server.Close()

	for name, tc := range map[string]struct {
		target  *redirectTarget
		wantErr string
	}{
		"https to http":    {target: plain, wantErr: "sent only over HTTPS"},
		"https other port": {target: otherPort, wantErr: "sent only to the configured API host"},
	} {
		t.Run(name, func(t *testing.T) {
			var starts atomic.Int32
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				starts.Add(1)
				w.Header().Set("Location", tc.target.server.URL+"/v1/users/svc/tokens")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer origin.Close()
			client, err := New(origin.URL, "admin-token", WithHTTPClient(origin.Client()))
			if err != nil {
				t.Fatal(err)
			}
			before := tc.target.requests.Load()
			_, err = client.ListTokens(context.Background(), "svc")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ListTokens error = %v, want one containing %q", err, tc.wantErr)
			}
			if got := tc.target.requests.Load() - before; got != 0 {
				t.Fatalf("redirect target received %d requests with Authorization %q, want none", got, tc.target.authorization.Load())
			}
			// A refused redirect is not a transient failure, so it is not retried.
			if got := starts.Load(); got != 1 {
				t.Fatalf("origin requests = %d, want 1", got)
			}
		})
	}
}

func TestCheckRedirectPolicy(t *testing.T) {
	request := func(raw string) *http.Request {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: parsed}
	}
	for name, tc := range map[string]struct {
		from, to string
		wantErr  bool
	}{
		"same https host":             {from: "https://api.motherduck.com/v1/users", to: "https://api.motherduck.com/v1/users/"},
		"https host letter case":      {from: "https://api.motherduck.com/a", to: "https://API.motherduck.com/b"},
		"https downgrade":             {from: "https://api.motherduck.com/a", to: "http://api.motherduck.com/a", wantErr: true},
		"https downgrade on loopback": {from: "https://127.0.0.1:8443/a", to: "http://127.0.0.1:8443/a", wantErr: true},
		"subdomain":                   {from: "https://api.motherduck.com/a", to: "https://evil.api.motherduck.com/a", wantErr: true},
		"other host":                  {from: "https://api.motherduck.com/a", to: "https://example.com/a", wantErr: true},
		"other port":                  {from: "https://api.motherduck.com/a", to: "https://api.motherduck.com:8443/a", wantErr: true},
		"plain loopback same host":    {from: "http://127.0.0.1:9000/a", to: "http://127.0.0.1:9000/b"},
		"plain loopback to remote":    {from: "http://127.0.0.1:9000/a", to: "http://api.motherduck.com/a", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkRedirect(nil)(request(tc.to), []*http.Request{request(tc.from)})
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("checkRedirect(%s -> %s) = %v, want error %t", tc.from, tc.to, err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, errRedirectRefused) {
				t.Fatalf("error %v does not wrap errRedirectRefused", err)
			}
		})
	}

	t.Run("redirect limit", func(t *testing.T) {
		via := make([]*http.Request, maxRedirects)
		for i := range via {
			via[i] = request("https://api.motherduck.com/a")
		}
		if err := checkRedirect(nil)(request("https://api.motherduck.com/b"), via); err == nil {
			t.Fatal("expected the redirect limit to stop the request")
		}
	})

	t.Run("caller policy still applies", func(t *testing.T) {
		called := false
		next := func(*http.Request, []*http.Request) error {
			called = true
			return http.ErrUseLastResponse
		}
		err := checkRedirect(next)(request("https://api.motherduck.com/b"), []*http.Request{request("https://api.motherduck.com/a")})
		if !called || !errors.Is(err, http.ErrUseLastResponse) {
			t.Fatalf("caller policy called=%t err=%v", called, err)
		}
	})
}

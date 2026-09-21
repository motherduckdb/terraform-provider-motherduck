package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

type requestReplayObservation struct {
	method        string
	body          []byte
	contentLength int64
	contentType   string
	accept        string
	userAgent     string
}

func TestRequestReplay_RetryPreservesPUTBodyAndHeaders(t *testing.T) {
	wantBody := []byte(`{"config":{"instance_size":"small"}}`)
	var requests []requestReplayObservation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests = append(requests, requestReplayObservation{
			method:        r.Method,
			body:          body,
			contentLength: r.ContentLength,
			contentType:   r.Header.Get("Content-Type"),
			accept:        r.Header.Get("Accept"),
			userAgent:     r.Header.Get("User-Agent"),
		})
		if len(requests) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token", WithUserAgent("replay-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.do(context.Background(), http.MethodPut, "/retry", json.RawMessage(wantBody), nil); err != nil {
		t.Fatal(err)
	}

	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	for i, request := range requests {
		if request.method != http.MethodPut {
			t.Errorf("request %d method = %q, want PUT", i+1, request.method)
		}
		if !bytes.Equal(request.body, wantBody) {
			t.Errorf("request %d body = %q, want %q", i+1, request.body, wantBody)
		}
		if request.contentLength != int64(len(wantBody)) {
			t.Errorf("request %d content length = %d, want %d", i+1, request.contentLength, len(wantBody))
		}
		if request.contentType != "application/json" {
			t.Errorf("request %d Content-Type = %q, want application/json", i+1, request.contentType)
		}
		if request.accept != "application/json" {
			t.Errorf("request %d Accept = %q, want application/json", i+1, request.accept)
		}
		if request.userAgent != "replay-test" {
			t.Errorf("request %d User-Agent = %q, want replay-test", i+1, request.userAgent)
		}
	}
}

func TestRequestReplay_RedirectPreservesPUTBody(t *testing.T) {
	wantBody := []byte(`{"config":{"instance_size":"small"}}`)
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var targetRequests []requestReplayObservation
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					w.Header().Set("Location", "/target")
					w.WriteHeader(status)
					return
				}
				if r.URL.Path != "/target" {
					t.Errorf("path = %q, want /target", r.URL.Path)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read redirected request body: %v", err)
				}
				targetRequests = append(targetRequests, requestReplayObservation{
					method:        r.Method,
					body:          body,
					contentLength: r.ContentLength,
					contentType:   r.Header.Get("Content-Type"),
				})
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client, err := New(server.URL, "admin-token")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.do(context.Background(), http.MethodPut, "/start", json.RawMessage(wantBody), nil); err != nil {
				t.Fatal(err)
			}

			if len(targetRequests) != 1 {
				t.Fatalf("target requests = %d, want 1", len(targetRequests))
			}
			request := targetRequests[0]
			if request.method != http.MethodPut {
				t.Errorf("redirected method = %q, want PUT", request.method)
			}
			if !bytes.Equal(request.body, wantBody) {
				t.Errorf("redirected body = %q, want %q", request.body, wantBody)
			}
			if request.contentLength != int64(len(wantBody)) {
				t.Errorf("redirected content length = %d, want %d", request.contentLength, len(wantBody))
			}
			if request.contentType != "application/json" {
				t.Errorf("redirected Content-Type = %q, want application/json", request.contentType)
			}
		})
	}
}

func TestRequestReplay_POSTDoesNotRetryAfter503(t *testing.T) {
	wantBody := []byte(`{"username":"svc"}`)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if !bytes.Equal(body, wantBody) {
			t.Errorf("request body = %q, want %q", body, wantBody)
		}
		if r.ContentLength != int64(len(wantBody)) {
			t.Errorf("content length = %d, want %d", r.ContentLength, len(wantBody))
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	err = client.do(context.Background(), http.MethodPost, "/create", json.RawMessage(wantBody), nil)
	if err == nil {
		t.Fatal("expected POST to return the 503")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %#v, want APIError with status 503", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

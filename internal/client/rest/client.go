package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

const DefaultBaseURL = "https://api.motherduck.com"

const (
	maxErrorBodyBytes   = 1 << 20
	maxSuccessBodyBytes = 16 << 20
	maxRetryAfterDelay  = 30 * time.Second
)

type Client struct {
	baseURL    string
	token      string
	userAgent  string
	httpClient *http.Client
}

// options collects Option values before the client is built, so the result
// does not depend on the order options are passed in.
type options struct {
	httpClient *http.Client
	timeout    time.Duration
	userAgent  string
}

type Option func(*options)

// WithHTTPClient uses a copy of httpClient for requests. The caller's client
// is never modified.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(o *options) {
		if httpClient != nil {
			o.httpClient = httpClient
		}
	}
}

// WithTimeout sets the per-request timeout. It applies whether it is passed
// before or after WithHTTPClient.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) {
		if timeout > 0 {
			o.timeout = timeout
		}
	}
}

func WithUserAgent(userAgent string) Option {
	return func(o *options) {
		o.userAgent = strings.TrimSpace(userAgent)
	}
}

func New(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	if err := ValidateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("base URL %s", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	if o.httpClient != nil {
		copied := *o.httpClient
		httpClient = &copied
	}
	if o.timeout > 0 {
		httpClient.Timeout = o.timeout
	}
	return &Client{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		// Trim once here: a token sourced from a file often carries a trailing
		// newline, which would otherwise surface as an opaque
		// "invalid header field value" error on every request.
		token:      strings.TrimSpace(token),
		userAgent:  o.userAgent,
		httpClient: httpClient,
	}, nil
}

func (c *Client) Available() bool {
	return c != nil && c.token != ""
}

func (c *Client) CreateServiceAccount(ctx context.Context, username string) (*ServiceAccount, error) {
	var out ServiceAccount
	err := c.do(ctx, http.MethodPost, "/v1/users", map[string]any{"username": username}, &out)
	return &out, err
}

func (c *Client) DeleteUser(ctx context.Context, username string) error {
	return c.do(ctx, http.MethodDelete, "/v1/users/"+url.PathEscape(username), nil, nil)
}

func (c *Client) CreateToken(ctx context.Context, username string, req CreateTokenRequest) (*Token, error) {
	var out Token
	err := c.do(ctx, http.MethodPost, "/v1/users/"+url.PathEscape(username)+"/tokens", req, &out)
	return &out, err
}

func (c *Client) ListTokens(ctx context.Context, username string) ([]Token, error) {
	basePath := "/v1/users/" + url.PathEscape(username) + "/tokens"
	return collectPages(basePath, func(path string) ([]Token, string, error) {
		var out ListTokensResponse
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, "", err
		}
		return out.Tokens, out.nextCursor(), nil
	})
}

func (c *Client) DeleteToken(ctx context.Context, username, tokenID string) error {
	path := "/v1/users/" + url.PathEscape(username) + "/tokens/" + url.PathEscape(tokenID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) GetDucklingConfig(ctx context.Context, username string) (*DucklingConfig, error) {
	var out DucklingConfig
	err := c.do(ctx, http.MethodGet, "/v1/users/"+url.PathEscape(username)+"/instances", nil, &out)
	return &out, err
}

func (c *Client) SetDucklingConfig(ctx context.Context, username string, cfg DucklingConfig) (*DucklingConfig, error) {
	var out DucklingConfig
	err := c.do(ctx, http.MethodPut, "/v1/users/"+url.PathEscape(username)+"/instances", map[string]any{"config": cfg}, &out)
	return &out, err
}

func (c *Client) ActiveAccounts(ctx context.Context) (*ActiveAccountsResponse, error) {
	basePath := "/v1/active_accounts"
	accounts, err := collectPages(basePath, func(path string) ([]ActiveAccount, string, error) {
		var out ActiveAccountsResponse
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, "", err
		}
		return out.Accounts, out.nextCursor(), nil
	})
	if err != nil {
		return nil, err
	}
	return &ActiveAccountsResponse{Accounts: accounts}, nil
}

func collectPages[T any](basePath string, fetch func(string) ([]T, string, error)) ([]T, error) {
	var result []T
	var cursor string
	seen := map[string]struct{}{}
	for {
		items, nextCursor, err := fetch(pathWithCursor(basePath, cursor))
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if nextCursor == "" {
			return result, nil
		}
		if _, ok := seen[nextCursor]; ok {
			return nil, fmt.Errorf("MotherDuck API pagination loop detected for %s", basePath)
		}
		seen[nextCursor] = struct{}{}
		cursor = nextCursor
	}
}

func (c *Client) CreateDiveEmbedSession(ctx context.Context, diveID string, req EmbedSessionRequest) (*EmbedSessionResponse, error) {
	var out EmbedSessionResponse
	err := c.do(ctx, http.MethodPost, "/v1/dives/"+url.PathEscape(diveID)+"/embed-session", req, &out)
	return &out, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) (err error) {
	if !c.Available() {
		return ErrMissingAdminToken
	}
	if err := validateRequestPath(path); err != nil {
		return err
	}

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	start := time.Now()
	res, err := c.doWithRetry(ctx, req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := res.Body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	tflog.Debug(ctx, "MotherDuck REST request completed", map[string]any{
		"method":      method,
		"path":        path,
		"status_code": res.StatusCode,
		"duration_ms": time.Since(start).Milliseconds(),
	})

	success := res.StatusCode >= 200 && res.StatusCode < 300
	maxBodyBytes := maxErrorBodyBytes
	if success {
		maxBodyBytes = maxSuccessBodyBytes
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(maxBodyBytes)+1))
	if err != nil {
		return err
	}
	truncated := len(data) > maxBodyBytes
	if truncated {
		data = data[:maxBodyBytes]
	}
	if !success {
		apiErr := &APIError{StatusCode: res.StatusCode, Body: string(data), BodyTruncated: truncated}
		_ = json.Unmarshal(data, apiErr)
		return apiErr
	}
	if truncated {
		return fmt.Errorf("MotherDuck API response exceeded the %d-byte safety limit", maxSuccessBodyBytes)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding MotherDuck API response: %w", err)
	}
	return nil
}

const maxAttempts = 4

func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	idempotent := retryableMethod(req.Method)
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		final := attempt == maxAttempts-1
		retryReq := req.Clone(ctx)
		if req.GetBody != nil {
			var err error
			retryReq.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
		res, err := c.httpClient.Do(retryReq)
		if err != nil {
			// A transport error can arrive after the server processed the
			// request, so only idempotent methods are retried.
			lastErr = err
			if !idempotent || final {
				return nil, err
			}
			if sleepErr := retry.Sleep(ctx, jitter(retryDelay(attempt+1))); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if final || !retryableStatus(req.Method, res.StatusCode) {
			return res, nil
		}
		delay := jitter(retryDelay(attempt + 1))
		if retryAfter := retryAfterDelay(res.Header.Get("Retry-After")); retryAfter > 0 {
			delay = retryAfter
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxErrorBodyBytes))
		_ = res.Body.Close()
		if err := retry.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func retryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodDelete, http.MethodPut:
		return true
	default:
		return false
	}
}

// retryableStatus reports whether a response status should be retried for a
// method. POST is never retried because MotherDuck does not document that a
// 429 or gateway error means the create was not processed, and a replayed
// create could mint a duplicate token or service account.
func retryableStatus(method string, statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return retryableMethod(method)
	default:
		return false
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(250*(1<<(attempt-1))) * time.Millisecond
}

// jitter spreads retries across the upper half of the backoff window so
// concurrent Terraform operations do not retry in lockstep.
func jitter(delay time.Duration) time.Duration {
	if delay <= 1 {
		return delay
	}
	half := delay / 2
	return half + rand.N(delay-half+1) // #nosec G404 -- retry jitter needs no cryptographic randomness.
}

func retryAfterDelay(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		if seconds >= int64(maxRetryAfterDelay/time.Second) {
			return maxRetryAfterDelay
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return min(delay, maxRetryAfterDelay)
		}
	}
	return 0
}

func pathWithCursor(path, cursor string) string {
	if cursor == "" {
		return path
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "cursor=" + url.QueryEscape(cursor)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

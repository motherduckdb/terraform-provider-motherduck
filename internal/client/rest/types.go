package rest

import (
	"errors"
	"fmt"
	"strings"
)

var ErrMissingAdminToken = errors.New("MotherDuck REST operations require admin_token or MOTHERDUCK_ADMIN_TOKEN")

type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Issues     []struct {
		Message string `json:"message"`
	} `json:"issues,omitempty"`
	Body          string `json:"-"`
	BodyTruncated bool   `json:"-"`
}

func (e APIError) Error() string {
	message := e.Message
	if message == "" {
		message = e.Body
	}
	issueMessages := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if strings.TrimSpace(issue.Message) != "" {
			issueMessages = append(issueMessages, issue.Message)
		}
	}
	if len(issueMessages) > 0 {
		issues := strings.Join(issueMessages, "; ")
		if message == "" {
			message = issues
		} else {
			message += ": " + issues
		}
	}
	if e.Code != "" {
		if e.BodyTruncated {
			message += " (response body truncated)"
		}
		return fmt.Sprintf("MotherDuck API error %d (%s): %s", e.StatusCode, e.Code, message)
	}
	if e.BodyTruncated {
		message += " (response body truncated)"
	}
	return fmt.Sprintf("MotherDuck API error %d: %s", e.StatusCode, message)
}

// IsEntityNotFound reports whether the error is a routed 404 for an entity that
// does not exist, as opposed to a 404 produced by a path with no route.
//
// MotherDuck answers a genuine miss with a JSON body carrying code "NOT_FOUND"
// and answers an unrouted path with a plain-text "Not Found" that has no code.
// A bare status check cannot tell those apart, so a wrong URL reads as a deleted
// resource and Terraform drops it from state on every refresh. Requiring the
// code means an unrecognized 404 surfaces as an error instead, which is the safe
// direction: a loud failure beats silently emptying state.
func (e APIError) IsEntityNotFound() bool {
	return e.StatusCode == 404 && e.Code == "NOT_FOUND"
}

type ServiceAccount struct {
	Username string `json:"username"`
}

type CreateTokenRequest struct {
	Name      string `json:"name"`
	TTL       *int64 `json:"ttl,omitempty"`
	TokenType string `json:"token_type,omitempty"`
}

type Token struct {
	Token     string `json:"token,omitempty"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	ExpireAt  string `json:"expire_at,omitempty"`
	CreatedTS string `json:"created_ts"`
	ReadOnly  bool   `json:"read_only"`
	TokenType string `json:"token_type"`
}

type ListTokensResponse struct {
	Tokens        []Token  `json:"tokens"`
	NextCursor    string   `json:"next_cursor,omitempty"`
	NextPageToken string   `json:"next_page_token,omitempty"`
	Pagination    pageInfo `json:"pagination,omitempty"`
}

type DucklingConfig struct {
	ReadWrite   DucklingReadWriteConfig   `json:"read_write"`
	ReadScaling DucklingReadScalingConfig `json:"read_scaling"`
}

type DucklingReadWriteConfig struct {
	InstanceSize    string `json:"instance_size"`
	CooldownSeconds *int64 `json:"cooldown_seconds,omitempty"`
}

type DucklingReadScalingConfig struct {
	InstanceSize    string  `json:"instance_size"`
	FlockSize       float64 `json:"flock_size"`
	CooldownSeconds *int64  `json:"cooldown_seconds,omitempty"`
}

type ActiveAccountsResponse struct {
	Accounts      []ActiveAccount `json:"accounts"`
	NextCursor    string          `json:"next_cursor,omitempty"`
	NextPageToken string          `json:"next_page_token,omitempty"`
	Pagination    pageInfo        `json:"pagination,omitempty"`
}

// pageInfo and the cursor fields on the list responses are forward-looking, not
// a description of current behavior. As verified against the live API, neither
// GET /v1/users/{username}/tokens nor GET /v1/active_accounts returns any cursor
// field, the published spec declares no pagination parameters for either, and a
// cursor query parameter is accepted and ignored. The loops in ListTokens and
// ActiveAccounts therefore make exactly one request today.
//
// Keep them anyway. accessTokenResource.Read treats a token missing from the
// listing as proof it was deleted and removes it from state, and that inference
// is only sound while the client walks every page. If MotherDuck starts capping
// list responses, unread pages would read as deleted tokens and Terraform would
// destroy and recreate live credentials. Several cursor spellings are accepted
// because the shape is unspecified; nextCursor() takes whichever appears.
type pageInfo struct {
	NextCursor    string `json:"next_cursor,omitempty"`
	NextPageToken string `json:"next_page_token,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

func (r ListTokensResponse) nextCursor() string {
	return firstNonEmpty(r.NextCursor, r.NextPageToken, r.Pagination.NextCursor, r.Pagination.NextPageToken, r.Pagination.Cursor)
}

func (r ActiveAccountsResponse) nextCursor() string {
	return firstNonEmpty(r.NextCursor, r.NextPageToken, r.Pagination.NextCursor, r.Pagination.NextPageToken, r.Pagination.Cursor)
}

type ActiveAccount struct {
	Username  string     `json:"username"`
	Ducklings []Duckling `json:"ducklings"`
}

type Duckling struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type EmbedSessionRequest struct {
	Username    string `json:"username"`
	SessionHint string `json:"session_hint,omitempty"`
}

type EmbedSessionResponse struct {
	Session string `json:"session"`
}

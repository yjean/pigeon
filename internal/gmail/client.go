// Package gmail is a small, dependency-free client for the Gmail REST API.
// It only implements what pigeon needs, which keeps the binary small and fast.
package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const baseURL = "https://gmail.googleapis.com/gmail/v1/users/me/"

// Client calls Gmail with an OAuth-authorized HTTP client (see auth.Client).
type Client struct{ hc *http.Client }

func New(hc *http.Client) *Client { return &Client{hc: hc} }

// Header is one RFC 822 header of a message part.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Part is a MIME part of a message.
type Part struct {
	PartID   string   `json:"partId"`
	MimeType string   `json:"mimeType"`
	Filename string   `json:"filename"`
	Headers  []Header `json:"headers"`
	Body     struct {
		Size         int    `json:"size"`
		Data         string `json:"data"`
		AttachmentID string `json:"attachmentId"`
	} `json:"body"`
	Parts []Part `json:"parts"`
}

// Message is a Gmail message (format=metadata or format=full).
type Message struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	HistoryID    string   `json:"historyId"`
	InternalDate string   `json:"internalDate"` // epoch ms, as a string
	Payload      Part     `json:"payload"`
}

// Thread is a conversation. From threads.list only ID, Snippet and HistoryID are set.
type Thread struct {
	ID        string    `json:"id"`
	Snippet   string    `json:"snippet"`
	HistoryID string    `json:"historyId"`
	Messages  []Message `json:"messages"`
}

// ListThreads returns the most recent threads carrying all labelIDs (none =
// all mail) and matching the Gmail search query (may be empty).
func (c *Client) ListThreads(ctx context.Context, labelIDs []string, query string, max int) ([]Thread, error) {
	threads, _, err := c.listThreads(ctx, labelIDs, query, max)
	return threads, err
}

// CountThreads estimates how many threads match (Gmail's resultSizeEstimate).
func (c *Client) CountThreads(ctx context.Context, labelIDs []string, query string) (int, error) {
	_, n, err := c.listThreads(ctx, labelIDs, query, 1)
	return n, err
}

func (c *Client) listThreads(ctx context.Context, labelIDs []string, query string, max int) ([]Thread, int, error) {
	q := url.Values{"maxResults": {fmt.Sprint(max)}}
	for _, l := range labelIDs {
		q.Add("labelIds", l)
	}
	if query != "" {
		q.Set("q", query)
	}
	var resp struct {
		Threads            []Thread `json:"threads"`
		ResultSizeEstimate int      `json:"resultSizeEstimate"`
	}
	err := c.do(ctx, http.MethodGet, "threads", q, nil, &resp)
	return resp.Threads, resp.ResultSizeEstimate, err
}

// GetThread fetches a thread: headers only (full=false) or with bodies (full=true).
func (c *Client) GetThread(ctx context.Context, id string, full bool) (*Thread, error) {
	q := url.Values{}
	if full {
		q.Set("format", "full")
	} else {
		q.Set("format", "metadata")
		for _, h := range []string{"From", "To", "Cc", "Subject", "Date"} {
			q.Add("metadataHeaders", h)
		}
	}
	var t Thread
	err := c.do(ctx, http.MethodGet, "threads/"+url.PathEscape(id), q, nil, &t)
	return &t, err
}

// ModifyThread adds/removes labels on every message of a thread.
func (c *Client) ModifyThread(ctx context.Context, id string, add, remove []string) error {
	body := map[string][]string{"addLabelIds": add, "removeLabelIds": remove}
	return c.do(ctx, http.MethodPost, "threads/"+url.PathEscape(id)+"/modify", nil, body, nil)
}

// ModifyMessage adds/removes labels on a single message.
func (c *Client) ModifyMessage(ctx context.Context, id string, add, remove []string) error {
	body := map[string][]string{"addLabelIds": add, "removeLabelIds": remove}
	return c.do(ctx, http.MethodPost, "messages/"+url.PathEscape(id)+"/modify", nil, body, nil)
}

// UnreadThreads returns the number of unread threads carrying a label (e.g. INBOX).
func (c *Client) UnreadThreads(ctx context.Context, label string) (int, error) {
	var resp struct {
		ThreadsUnread int `json:"threadsUnread"`
	}
	err := c.do(ctx, http.MethodGet, "labels/"+url.PathEscape(label), nil, nil, &resp)
	return resp.ThreadsUnread, err
}

// TrashThread moves a thread to the trash.
func (c *Client) TrashThread(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "threads/"+url.PathEscape(id)+"/trash", nil, nil, nil)
}

// UntrashThread restores a thread from the trash.
func (c *Client) UntrashThread(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "threads/"+url.PathEscape(id)+"/untrash", nil, nil, nil)
}

// APIError is a non-2xx Gmail response.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("gmail: %d %s", e.Status, e.Message) }

func (e *APIError) retryable() bool {
	switch e.Status {
	case 429, 500, 502, 503, 504:
		return true
	case 403:
		return e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded"
	}
	return false
}

// do performs a request, retrying rate limits and transient errors with exponential backoff.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	u := baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	backoff := 400 * time.Millisecond
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode < 300 {
			defer resp.Body.Close()
			if out == nil {
				return nil
			}
			return json.NewDecoder(resp.Body).Decode(out)
		}
		apiErr := parseError(resp)
		if attempt >= 4 || !apiErr.retryable() {
			return apiErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
}

func parseError(resp *http.Response) *APIError {
	defer resp.Body.Close()
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(data, &e)
	out := &APIError{Status: resp.StatusCode, Message: e.Error.Message}
	if out.Message == "" {
		out.Message = resp.Status
	}
	if len(e.Error.Errors) > 0 {
		out.Reason = e.Error.Errors[0].Reason
	}
	return out
}

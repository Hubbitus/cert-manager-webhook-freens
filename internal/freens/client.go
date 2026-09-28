// Package freens is a minimal client for the FreeNS DNS API
// (https://www.freens.ru/api-docs).
package freens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://freens.ru/api/v1"
	DefaultTimeout = 30 * time.Second

	// ApexName is how FreeNS names the zone apex in a record's `name` field.
	// Source: API docs ("Names use `@` for apex/root domain"), checked
	// 2026-09-28; pending confirmation against the live API.
	ApexName = "@"
)

// ErrZoneNotFound is returned by DomainID when the account has no such zone.
var ErrZoneNotFound = errors.New("zone not found in FreeNS account")

// Record is a DNS record. Name is relative to the zone.
type Record struct {
	ID      int    `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
}

// APIError is a non-2xx answer from the API.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("freens: %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("freens: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		APIKey:  apiKey,
		HTTP: &http.Client{
			Timeout: DefaultTimeout,
			// Never follow redirects: the X-API-Key header would go along to
			// whatever host or scheme the Location names.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// DomainID returns the FreeNS id of zone; a trailing dot and case are ignored.
func (c *Client) DomainID(ctx context.Context, zone string) (int, error) {
	var resp struct {
		Domains []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"domains"`
	}
	if err := c.do(ctx, http.MethodGet, "/domains", nil, &resp); err != nil {
		return 0, err
	}
	want := strings.TrimSuffix(zone, ".")
	for _, d := range resp.Domains {
		if strings.EqualFold(d.Name, want) {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("%w: %s", ErrZoneNotFound, want)
}

func (c *Client) Records(ctx context.Context, domainID int) ([]Record, error) {
	var resp struct {
		Records []Record `json:"records"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/domains/%d/records", domainID), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Records, nil
}

// CreateRecord creates r; the response body is not used.
func (c *Client) CreateRecord(ctx context.Context, domainID int, r Record) error {
	r.ID = 0
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/domains/%d/records", domainID), &r, nil)
}

// DeleteRecord deletes a record; an already deleted record (404) is not an error.
func (c *Client) DeleteRecord(ctx context.Context, domainID, recordID int) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/domains/%d/records/%d", domainID, recordID), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, in *Record, out any) error {
	var body io.Reader
	if in != nil {
		// Cannot fail: Record has only string and int fields.
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("freens: %s %s: %w", method, path, err)
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Transport errors carry the URL, never headers.
		return fmt.Errorf("freens: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("freens: %s %s: HTTP %d: read body: %w", method, path, resp.StatusCode, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Message: c.redact(e.Error)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("freens: %s %s: HTTP %d: decode response: %w", method, path, resp.StatusCode, err)
	}
	return nil
}

// redact removes the API key from text the server echoes back.
func (c *Client) redact(s string) string {
	if c.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.APIKey, "[REDACTED]")
}

package freens

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testKey = "fns_test_secret_key_value"

type request struct {
	Method string
	Path   string
	APIKey string
	Body   string
}

// fakeAPI serves canned responses per "METHOD /path" and records every request.
type fakeAPI struct {
	mu        sync.Mutex
	requests  []request
	responses map[string]response
}

type response struct {
	status int
	body   string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, request{r.Method, r.URL.Path, r.Header.Get("X-API-Key"), string(body)})
	f.mu.Unlock()
	resp, ok := f.responses[r.Method+" "+r.URL.Path]
	if !ok {
		resp = response{http.StatusNotFound, `{"error":"no route in fake"}`}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = io.WriteString(w, resp.body)
}

func newClient(t *testing.T, responses map[string]response) (*Client, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{responses: responses}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testKey), api
}

func assertNoKey(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks API key: %q", err)
	}
}

const domainsBody = `{"domains":[
	{"id":7,"user_id":756,"name":"other.example.org","is_active":true},
	{"id":55,"user_id":756,"name":"dev.neinache.com","is_active":true}]}`

func TestDomainIDFindsZone(t *testing.T) {
	c, _ := newClient(t, map[string]response{"GET /domains": {200, domainsBody}})

	id, err := c.DomainID(context.Background(), "dev.neinache.com")
	if err != nil {
		t.Fatalf("DomainID: %v", err)
	}
	if id != 55 {
		t.Fatalf("id = %d, want 55", id)
	}
}

func TestDomainIDAcceptsTrailingDotAndCase(t *testing.T) {
	c, _ := newClient(t, map[string]response{"GET /domains": {200, domainsBody}})

	id, err := c.DomainID(context.Background(), "Dev.Neinache.com.")
	if err != nil || id != 55 {
		t.Fatalf("DomainID = %d, %v; want 55, nil", id, err)
	}
}

func TestDomainIDZoneMissing(t *testing.T) {
	c, _ := newClient(t, map[string]response{"GET /domains": {200, domainsBody}})

	_, err := c.DomainID(context.Background(), "absent.example.com")
	if !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("err = %v, want ErrZoneNotFound", err)
	}
	if !strings.Contains(err.Error(), "absent.example.com") {
		t.Fatalf("error %q does not name the zone", err)
	}
}

func TestAPIKeyHeaderOnEveryRequest(t *testing.T) {
	c, api := newClient(t, map[string]response{
		"GET /domains":                 {200, domainsBody},
		"GET /domains/55/records":      {200, `{"records":[]}`},
		"POST /domains/55/records":     {201, `{"record":{"id":1,"name":"a","type":"TXT","content":"k","ttl":60}}`},
		"DELETE /domains/55/records/1": {200, `{}`},
	})
	ctx := context.Background()

	if _, err := c.DomainID(ctx, "dev.neinache.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Records(ctx, 55); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateRecord(ctx, 55, Record{Name: "a", Type: "TXT", Content: "k", TTL: 60}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(ctx, 55, 1); err != nil {
		t.Fatal(err)
	}

	if len(api.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(api.requests))
	}
	for _, r := range api.requests {
		if r.APIKey != testKey {
			t.Errorf("%s %s: X-API-Key = %q", r.Method, r.Path, r.APIKey)
		}
	}
}

func TestErrorsDoNotLeakAPIKey(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"GET /domains": {401, `{"error":"invalid api key ` + testKey + `"}`},
	})

	_, err := c.DomainID(context.Background(), "dev.neinache.com")
	if err == nil {
		t.Fatal("want error on 401")
	}
	assertNoKey(t, err)
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error %q lacks HTTP status", err)
	}
}

func TestRecordsParsesList(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"GET /domains/55/records": {200, `{"records":[
			{"id":120,"domain_id":55,"name":"_acme-challenge","type":"TXT","content":"k1","ttl":60,"priority":null},
			{"id":121,"domain_id":55,"name":"*","type":"A","content":"1.2.3.4","ttl":60}]}`},
	})

	recs, err := c.Records(context.Background(), 55)
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{ID: 120, Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60},
		{ID: 121, Name: "*", Type: "A", Content: "1.2.3.4", TTL: 60},
	}
	if len(recs) != len(want) {
		t.Fatalf("records = %+v, want %+v", recs, want)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("record[%d] = %+v, want %+v", i, recs[i], want[i])
		}
	}
}

func TestCreateRecordSendsExactBody(t *testing.T) {
	c, api := newClient(t, map[string]response{
		"POST /domains/55/records": {201, `{"record":{"id":130,"domain_id":55,"name":"_acme-challenge","type":"TXT","content":"k","ttl":60}}`},
	})

	// A stale ID on the input must not leak into the create request.
	if err := c.CreateRecord(context.Background(), 55, Record{ID: 5, Name: "_acme-challenge", Type: "TXT", Content: "k", TTL: 60}); err != nil {
		t.Fatal(err)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(api.requests[0].Body), &sent); err != nil {
		t.Fatalf("body %q: %v", api.requests[0].Body, err)
	}
	want := map[string]any{"name": "_acme-challenge", "type": "TXT", "content": "k", "ttl": float64(60)}
	if len(sent) != len(want) {
		t.Fatalf("body = %v, want %v", sent, want)
	}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, sent[k], v)
		}
	}
}

// The create response is undocumented and unused: any 2xx body is success.
func TestCreateRecordIgnoresResponseBody(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"POST /domains/55/records": {201, `not json`},
	})

	if err := c.CreateRecord(context.Background(), 55, Record{Name: "x", Type: "TXT", Content: "k", TTL: 60}); err != nil {
		t.Fatalf("CreateRecord = %v, want nil", err)
	}
}

func TestDeleteRecordNotFoundIsNil(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"DELETE /domains/55/records/9": {404, `{"error":"Record not found"}`},
	})

	if err := c.DeleteRecord(context.Background(), 55, 9); err != nil {
		t.Fatalf("DeleteRecord on 404 = %v, want nil", err)
	}
}

func TestDeleteRecordServerError(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"DELETE /domains/55/records/9": {500, `{"error":"boom"}`},
	})

	err := c.DeleteRecord(context.Background(), 55, 9)
	if err == nil {
		t.Fatal("want error on 500")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want *APIError with status 500", err)
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error %q lacks message or status", err)
	}
	assertNoKey(t, err)
}

func TestNonJSONResponse(t *testing.T) {
	c, _ := newClient(t, map[string]response{
		"GET /domains/55/records": {502, `<html>Bad Gateway</html>`},
		"GET /domains":            {200, `not json`},
	})

	_, err := c.Records(context.Background(), 55)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Records err = %v, want error with 502", err)
	}
	_, err = c.DomainID(context.Background(), "dev.neinache.com")
	if err == nil || !strings.Contains(err.Error(), "200") {
		t.Fatalf("DomainID err = %v, want decode error with 200", err)
	}
}

func TestNewClientTimeout(t *testing.T) {
	c := NewClient(DefaultBaseURL, testKey)
	if c.HTTP == nil || c.HTTP.Timeout != DefaultTimeout {
		t.Fatalf("HTTP client timeout not set to %s", DefaultTimeout)
	}
	if DefaultBaseURL != "https://freens.ru/api/v1" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestTransportErrorDoesNotLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := NewClient(srv.URL, testKey)

	_, err := c.Records(context.Background(), 55)
	if err == nil {
		t.Fatal("want transport error")
	}
	assertNoKey(t, err)
}

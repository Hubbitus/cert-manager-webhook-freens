package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/Hubbitus/cert-manager-webhook-freens/internal/freens"
)

const (
	testKey    = "fns_solver_test_secret"
	testNS     = "cert-manager"
	testSecret = "freens-api-key"
	testZone   = "example.org"
	testID     = 2
)

// fakeFreeNS is an in-memory FreeNS account with one zone.
type fakeFreeNS struct {
	mu         sync.Mutex
	nextID     int
	records    map[int]freens.Record
	posts      int
	deletes    []int
	failKey    bool // answer 401 echoing the key
	failWrites bool // answer 500 to POST and DELETE
	attempts   int  // POST and DELETE requests, failed ones included
	failList   bool // answer 500 to GET records
	transport  http.RoundTripper
}

func newFakeFreeNS(t *testing.T, seed ...freens.Record) (*fakeFreeNS, string) {
	t.Helper()
	f := &fakeFreeNS{nextID: 100, records: map[int]freens.Record{}}
	for _, r := range seed {
		f.add(r)
	}
	// TLS: the solver refuses a non-https apiUrl.
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	f.transport = srv.Client().Transport
	return f, srv.URL
}

func (f *fakeFreeNS) add(r freens.Record) freens.Record {
	f.nextID++
	r.ID = f.nextID
	f.records[r.ID] = r
	return r
}

func (f *fakeFreeNS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("X-API-Key") != testKey || f.failKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":"invalid key %s"}`, r.Header.Get("X-API-Key"))
		return
	}
	recordsPath := fmt.Sprintf("/domains/%d/records", testID)
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		f.attempts++
		if f.failWrites {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
			return
		}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/domains":
		_, _ = fmt.Fprintf(w, `{"domains":[{"id":%d,"name":%q}]}`, testID, testZone)
	case r.Method == http.MethodGet && r.URL.Path == recordsPath && f.failList:
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"list failed"}`)
	case r.Method == http.MethodGet && r.URL.Path == recordsPath:
		list := make([]freens.Record, 0, len(f.records))
		for _, rec := range f.records {
			list = append(list, rec)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"records": list})
	case r.Method == http.MethodPost && r.URL.Path == recordsPath:
		var rec freens.Record
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &rec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.posts++
		rec = f.add(rec)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"record": rec})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, recordsPath+"/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, recordsPath+"/"))
		if _, ok := f.records[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"Record not found"}`)
			return
		}
		delete(f.records, id)
		f.deletes = append(f.deletes, id)
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Not found"}`)
	}
}

func (f *fakeFreeNS) txt(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.records {
		if r.Type == "TXT" && r.Name == name {
			out = append(out, r.Content)
		}
	}
	return out
}

func apiKeySecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: testSecret},
		Data:       data,
	}
}

// newSolver returns a solver that trusts api's TLS certificate.
func newSolver(api *fakeFreeNS, objs ...*corev1.Secret) *freensSolver {
	cs := fake.NewClientset()
	for _, o := range objs {
		_ = cs.Tracker().Add(o)
	}
	return &freensSolver{client: cs, transport: api.transport}
}

func challenge(t *testing.T, apiURL, key string, extra map[string]any) *v1alpha1.ChallengeRequest {
	t.Helper()
	cfg := map[string]any{
		"apiKeySecretRef": map[string]any{"name": testSecret, "key": "api-key"},
		"apiUrl":          apiURL,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &v1alpha1.ChallengeRequest{
		ResourceNamespace: testNS,
		ResolvedFQDN:      "_acme-challenge." + testZone + ".",
		ResolvedZone:      testZone + ".",
		Key:               key,
		Config:            &extapi.JSON{Raw: raw},
	}
}

var validSecret = apiKeySecret(map[string][]byte{"api-key": []byte(testKey)})

func TestName(t *testing.T) {
	if got := (&freensSolver{}).Name(); got != "freens" {
		t.Fatalf("Name() = %q, want freens", got)
	}
}

func TestPresentCreatesOneTXTWithTTL60(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)

	if err := s.Present(challenge(t, url, "k1", nil)); err != nil {
		t.Fatalf("Present: %v", err)
	}

	if api.posts != 1 {
		t.Fatalf("POSTs = %d, want 1", api.posts)
	}
	for _, r := range api.records {
		if r.Name != "_acme-challenge" || r.Type != "TXT" || r.Content != "k1" || r.TTL != 60 {
			t.Fatalf("record = %+v, want _acme-challenge TXT k1 ttl 60", r)
		}
	}
}

func TestPresentIsIdempotent(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", nil)

	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}

	if api.posts != 1 {
		t.Fatalf("POSTs after repeated Present = %d, want 1", api.posts)
	}
}

// Review Focus #2: example.org and *.example.org share one _acme-challenge name.
func TestParallelChallengesOnOneName(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	apex := challenge(t, url, "key-apex", nil)
	wild := challenge(t, url, "key-wildcard", nil)

	if err := s.Present(apex); err != nil {
		t.Fatal(err)
	}
	if err := s.Present(wild); err != nil {
		t.Fatal(err)
	}
	if got := api.txt("_acme-challenge"); len(got) != 2 {
		t.Fatalf("TXT after two Present = %v, want 2 values", got)
	}

	if err := s.CleanUp(apex); err != nil {
		t.Fatal(err)
	}
	got := api.txt("_acme-challenge")
	if len(got) != 1 || got[0] != "key-wildcard" {
		t.Fatalf("TXT after CleanUp(apex) = %v, want [key-wildcard]", got)
	}
}

// Review Focus #3: no in-memory state survives between Present and CleanUp.
func TestCleanUpByFreshInstance(t *testing.T) {
	api, url := newFakeFreeNS(t)
	ch := challenge(t, url, "k1", nil)

	if err := newSolver(api, validSecret).Present(ch); err != nil {
		t.Fatal(err)
	}
	if err := newSolver(api, validSecret).CleanUp(ch); err != nil {
		t.Fatal(err)
	}

	if got := api.txt("_acme-challenge"); len(got) != 0 {
		t.Fatalf("TXT after CleanUp by new instance = %v, want none", got)
	}
}

func TestCleanUpTouchesOnlyMatchingTXT(t *testing.T) {
	api, url := newFakeFreeNS(t,
		freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60},
		freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60}, // duplicate from a racing Present
		freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "other", TTL: 60},
		freens.Record{Name: "other", Type: "TXT", Content: "k1", TTL: 60},
		freens.Record{Name: "_acme-challenge", Type: "CNAME", Content: "k1", TTL: 60},
		freens.Record{Name: "*", Type: "A", Content: "1.2.3.4", TTL: 60},
	)
	s := newSolver(api, validSecret)

	if err := s.CleanUp(challenge(t, url, "k1", nil)); err != nil {
		t.Fatal(err)
	}

	if len(api.deletes) != 2 {
		t.Fatalf("deleted ids = %v, want the two matching TXT", api.deletes)
	}
	if len(api.records) != 4 {
		t.Fatalf("remaining = %+v, want 4 untouched records", api.records)
	}
}

func TestCleanUpWithoutRecordIsNil(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)

	if err := s.CleanUp(challenge(t, url, "k1", nil)); err != nil {
		t.Fatalf("CleanUp on empty zone = %v, want nil", err)
	}
	if len(api.deletes) != 0 {
		t.Fatalf("deletes = %v, want none", api.deletes)
	}
}

func TestSecretMissing(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api)

	err := s.Present(challenge(t, url, "k1", nil))
	if err == nil {
		t.Fatal("want error when Secret is absent")
	}
	for _, want := range []string{testNS + "/" + testSecret, "api-key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestSecretKeyMissing(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, apiKeySecret(map[string][]byte{"other": []byte(testKey)}))

	err := s.Present(challenge(t, url, "k1", nil))
	if err == nil {
		t.Fatal("want error when Secret lacks the key")
	}
	for _, want := range []string{testNS + "/" + testSecret, "api-key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks Secret value: %q", err)
	}
}

func TestSecretRefIncomplete(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", map[string]any{"apiKeySecretRef": map[string]any{"name": testSecret}})

	if err := s.Present(ch); err == nil || !strings.Contains(err.Error(), "apiKeySecretRef") {
		t.Fatalf("err = %v, want apiKeySecretRef validation error", err)
	}
}

func TestZoneNotInAccount(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", nil)
	ch.ResolvedFQDN = "_acme-challenge.absent.example.com."
	ch.ResolvedZone = "absent.example.com."

	err := s.Present(ch)
	if err == nil || !strings.Contains(err.Error(), "absent.example.com") {
		t.Fatalf("err = %v, want error naming absent.example.com", err)
	}
}

// `zone` overrides ResolvedZone, e.g. for conformance records under a ci.
// label that live inside the parent FreeNS domain.
func TestZoneOverride(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", map[string]any{"zone": testZone})
	ch.ResolvedFQDN = "cert-manager-dns01-tests.ci." + testZone + "."
	ch.ResolvedZone = "ci." + testZone + "."

	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	if got := api.txt("cert-manager-dns01-tests.ci"); len(got) != 1 {
		t.Fatalf("TXT cert-manager-dns01-tests.ci = %v, want one", got)
	}
}

func TestFQDNOutsideZone(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", nil)
	ch.ResolvedFQDN = "_acme-challenge.example.net."

	if err := s.Present(ch); err == nil || !strings.Contains(err.Error(), "example.net") {
		t.Fatalf("err = %v, want error for FQDN outside zone", err)
	}
}

func TestAPIErrorDoesNotLeakKey(t *testing.T) {
	api, url := newFakeFreeNS(t)
	api.failKey = true
	s := newSolver(api, validSecret)

	err := s.Present(challenge(t, url, "k1", nil))
	if err == nil {
		t.Fatal("want error on 401")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error leaks API key: %q", err)
	}
}

func TestDefaultAPIURL(t *testing.T) {
	cfg, err := loadConfig(&extapi.JSON{Raw: []byte(`{"apiKeySecretRef":{"name":"n","key":"k"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != freens.DefaultBaseURL {
		t.Fatalf("APIURL = %q, want %q", cfg.APIURL, freens.DefaultBaseURL)
	}
}

func TestConfigMissingOrMalformed(t *testing.T) {
	if _, err := loadConfig(nil); err == nil {
		t.Error("nil config: want error (apiKeySecretRef is required)")
	}
	if _, err := loadConfig(&extapi.JSON{Raw: []byte(`{`)}); err == nil {
		t.Error("malformed JSON: want error")
	}
}

func TestInitializeBuildsClient(t *testing.T) {
	s := &freensSolver{}
	if err := s.Initialize(&rest.Config{Host: "https://127.0.0.1:6443"}, nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if s.client == nil {
		t.Fatal("Initialize left client nil")
	}
}

func TestGroupName(t *testing.T) {
	t.Setenv("GROUP_NAME", "")
	if got := groupName(); got != DefaultGroupName {
		t.Errorf("groupName() = %q, want %q", got, DefaultGroupName)
	}
	t.Setenv("GROUP_NAME", "acme.example.org")
	if got := groupName(); got != "acme.example.org" {
		t.Errorf("groupName() = %q, want acme.example.org", got)
	}
}

func TestPresentReportsCreateFailure(t *testing.T) {
	api, url := newFakeFreeNS(t)
	api.failWrites = true
	s := newSolver(api, validSecret)

	err := s.Present(challenge(t, url, "k1", nil))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want API error with boom", err)
	}
}

func TestCleanUpTriesEveryMatchAndReportsFailures(t *testing.T) {
	api, url := newFakeFreeNS(t,
		freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60},
		freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60},
	)
	api.failWrites = true
	s := newSolver(api, validSecret)

	err := s.CleanUp(challenge(t, url, "k1", nil))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want API error with boom", err)
	}
	if api.attempts != 2 {
		t.Fatalf("DELETE attempts = %d, want 2", api.attempts)
	}
}

func TestCleanUpConfigError(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api)

	if err := s.CleanUp(challenge(t, url, "k1", nil)); err == nil {
		t.Fatal("want error when Secret is absent")
	}
}

func TestAPIURLMustBeHTTPS(t *testing.T) {
	for _, u := range []string{"http://freens.example", "ftp://freens.example", "https://", "freens.example/api"} {
		raw := []byte(`{"apiKeySecretRef":{"name":"n","key":"k"},"apiUrl":"` + u + `"}`)
		if _, err := loadConfig(&extapi.JSON{Raw: raw}); err == nil || !strings.Contains(err.Error(), "apiUrl") {
			t.Errorf("apiUrl %q: err = %v, want apiUrl rejected", u, err)
		}
	}
	raw := []byte(`{"apiKeySecretRef":{"name":"n","key":"k"},"apiUrl":"https://freens.example/api/v1"}`)
	if _, err := loadConfig(&extapi.JSON{Raw: raw}); err != nil {
		t.Errorf("https apiUrl: %v", err)
	}
}

func TestInitializeError(t *testing.T) {
	s := &freensSolver{}
	// client-go rejects QPS without Burst when no RateLimiter is set.
	if err := s.Initialize(&rest.Config{Host: "https://127.0.0.1:6443", QPS: 5}, nil); err == nil {
		t.Fatal("Initialize with invalid rest.Config: want error")
	}
	if s.client != nil {
		t.Fatal("client set despite error")
	}
}

func TestPresentReportsListFailure(t *testing.T) {
	api, url := newFakeFreeNS(t)
	api.failList = true
	s := newSolver(api, validSecret)

	err := s.Present(challenge(t, url, "k1", nil))
	if err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("err = %v, want list error", err)
	}
	if api.attempts != 0 {
		t.Fatalf("writes after failed list = %d, want 0", api.attempts)
	}
}

func TestCleanUpReportsListFailure(t *testing.T) {
	api, url := newFakeFreeNS(t, freens.Record{Name: "_acme-challenge", Type: "TXT", Content: "k1", TTL: 60})
	api.failList = true
	s := newSolver(api, validSecret)

	err := s.CleanUp(challenge(t, url, "k1", nil))
	if err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("err = %v, want list error", err)
	}
	if api.attempts != 0 {
		t.Fatalf("writes after failed list = %d, want 0", api.attempts)
	}
}

// A challenge FQDN equal to the zone maps to the FreeNS apex name.
func TestApexFQDN(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", nil)
	ch.ResolvedFQDN = testZone + "."

	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	if got := api.txt(freens.ApexName); len(got) != 1 {
		t.Fatalf("TXT %q = %v, want one", freens.ApexName, got)
	}
}

func TestMixedCaseFQDNAndZone(t *testing.T) {
	api, url := newFakeFreeNS(t)
	s := newSolver(api, validSecret)
	ch := challenge(t, url, "k1", nil)
	ch.ResolvedFQDN = "_ACME-Challenge.Example.ORG."
	ch.ResolvedZone = "Example.Org."

	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	if got := api.txt("_acme-challenge"); len(got) != 1 {
		t.Fatalf("TXT _acme-challenge = %v, want one", got)
	}
}

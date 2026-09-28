//go:build conformance

// Conformance suite of cert-manager against the live FreeNS API. It is not part
// of `go test ./...`; run it with `make test-conformance`.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"testing"
	"time"

	acmetest "github.com/cert-manager/cert-manager/test/acme"
)

const (
	// Records go to the dev.neinache.com FreeNS domain (config.json `zone`)
	// under the ci. label, apart from real _acme-challenge records (ADR-0076).
	conformanceZone = "ci.dev.neinache.com."
	// Authoritative FreeNS server: the test works before delegation.
	conformanceDNS = "a.freens.ru:53"
)

func TestConformance(t *testing.T) {
	// A unique name per run keeps concurrent runs from deleting each other's records.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	fixture := acmetest.NewFixture(&freensSolver{},
		acmetest.SetResolvedZone(conformanceZone),
		acmetest.SetResolvedFQDN("cert-manager-dns01-tests-"+hex.EncodeToString(suffix)+"."+conformanceZone),
		acmetest.SetAllowAmbientCredentials(false),
		acmetest.SetManifestPath("testdata/freens"),
		acmetest.SetDNSServer(conformanceDNS),
		acmetest.SetStrict(true),
		acmetest.SetPropagationLimit(3*time.Minute),
	)
	// The fixture raises klog verbosity to 12, at which client-go dumps
	// response bodies, including the API key Secret. Keep it quiet.
	if v := flag.Lookup("v"); v != nil {
		if err := v.Value.Set("0"); err != nil {
			t.Fatal(err)
		}
	}
	fixture.RunConformance(t)
}

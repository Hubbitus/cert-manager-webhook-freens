package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/Hubbitus/cert-manager-webhook-freens/internal/freens"
)

const (
	recordTTL   = 60
	callTimeout = 2 * time.Minute
)

// freensSolver presents and cleans up ACME DNS-01 TXT records in FreeNS.
//
// It keeps no state between calls: Present and CleanUp find records by
// (name, type TXT, content == challenge key) in a fresh zone listing, so a
// pod restart in between leaves nothing behind and concurrent challenges
// for the same name never touch each other's records.
type freensSolver struct {
	client kubernetes.Interface
}

var _ webhook.Solver = (*freensSolver)(nil)

// Name is the solverName referenced in the Issuer's webhook stanza.
func (s *freensSolver) Name() string {
	return "freens"
}

func (s *freensSolver) Initialize(kubeClientConfig *rest.Config, _ <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}
	s.client = cl
	return nil
}

// Present creates the TXT record unless one with the same name and value exists.
func (s *freensSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	t, err := s.target(ctx, ch)
	if err != nil {
		return err
	}
	records, err := t.api.Records(ctx, t.domainID)
	if err != nil {
		return err
	}
	if len(matching(records, t.name, ch.Key)) > 0 {
		klog.InfoS("TXT record already present", "zone", t.zone, "name", t.name)
		return nil
	}
	rec := freens.Record{Name: t.name, Type: "TXT", Content: ch.Key, TTL: recordTTL}
	if _, err := t.api.CreateRecord(ctx, t.domainID, rec); err != nil {
		return err
	}
	klog.InfoS("TXT record created", "zone", t.zone, "name", t.name)
	return nil
}

// CleanUp deletes every TXT record with the challenge's name and value.
func (s *freensSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	t, err := s.target(ctx, ch)
	if err != nil {
		return err
	}
	records, err := t.api.Records(ctx, t.domainID)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range matching(records, t.name, ch.Key) {
		if err := t.api.DeleteRecord(ctx, t.domainID, r.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		klog.InfoS("TXT record deleted", "zone", t.zone, "name", t.name, "id", r.ID)
	}
	return errors.Join(errs...)
}

// target is where a challenge's record lives in FreeNS.
type target struct {
	api      *freens.Client
	zone     string
	domainID int
	name     string
}

func (s *freensSolver) target(ctx context.Context, ch *v1alpha1.ChallengeRequest) (target, error) {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return target{}, err
	}
	zone := normalize(cfg.Zone)
	if zone == "" {
		zone = normalize(ch.ResolvedZone)
	}
	name, err := relativeName(ch.ResolvedFQDN, zone)
	if err != nil {
		return target{}, err
	}
	key, err := s.apiKey(ctx, ch.ResourceNamespace, cfg)
	if err != nil {
		return target{}, err
	}
	api := freens.NewClient(cfg.APIURL, key)
	id, err := api.DomainID(ctx, zone)
	if err != nil {
		return target{}, err
	}
	return target{api: api, zone: zone, domainID: id, name: name}, nil
}

func normalize(domain string) string {
	return strings.ToLower(strings.TrimSuffix(domain, "."))
}

// relativeName turns an FQDN into a record name relative to zone.
func relativeName(fqdn, zone string) (string, error) {
	f := normalize(fqdn)
	switch {
	case f == zone:
		return freens.ApexName, nil
	case strings.HasSuffix(f, "."+zone):
		return strings.TrimSuffix(f, "."+zone), nil
	default:
		return "", fmt.Errorf("FQDN %s is not inside zone %s", f, zone)
	}
}

func (s *freensSolver) apiKey(ctx context.Context, namespace string, cfg config) (string, error) {
	ref := namespace + "/" + cfg.APIKeySecretRef.Name
	sec, err := s.client.CoreV1().Secrets(namespace).Get(ctx, cfg.APIKeySecretRef.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read FreeNS API key Secret %s (key %q): %w", ref, cfg.APIKeySecretRef.Key, err)
	}
	v := strings.TrimSpace(string(sec.Data[cfg.APIKeySecretRef.Key]))
	if v == "" {
		return "", fmt.Errorf("FreeNS API key Secret %s has no non-empty key %q", ref, cfg.APIKeySecretRef.Key)
	}
	return v, nil
}

func matching(records []freens.Record, name, key string) []freens.Record {
	var out []freens.Record
	for _, r := range records {
		if strings.EqualFold(r.Type, "TXT") && strings.EqualFold(r.Name, name) && r.Content == key {
			out = append(out, r)
		}
	}
	return out
}

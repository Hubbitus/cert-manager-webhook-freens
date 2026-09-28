package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/Hubbitus/cert-manager-webhook-freens/internal/freens"
)

// config is the solver's `webhook.config` stanza on the Issuer.
type config struct {
	// APIKeySecretRef points to the FreeNS API key. The Secret is read from
	// the challenge's resource namespace (cert-manager's cluster resource
	// namespace for a ClusterIssuer).
	APIKeySecretRef cmmeta.SecretKeySelector `json:"apiKeySecretRef"`
	// Zone is the FreeNS domain the records go to. Defaults to the zone
	// cert-manager resolved via SOA lookup.
	Zone string `json:"zone,omitempty"`
	// APIURL must be https; defaults to freens.DefaultBaseURL.
	APIURL string `json:"apiUrl,omitempty"`
}

func loadConfig(cfgJSON *extapi.JSON) (config, error) {
	cfg := config{}
	if cfgJSON == nil {
		return cfg, errors.New("solver config is empty: apiKeySecretRef.name and apiKeySecretRef.key are required")
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decode solver config: %w", err)
	}
	if cfg.APIKeySecretRef.Name == "" || cfg.APIKeySecretRef.Key == "" {
		return cfg, errors.New("solver config: apiKeySecretRef.name and apiKeySecretRef.key are required")
	}
	if cfg.APIURL == "" {
		cfg.APIURL = freens.DefaultBaseURL
	}
	if u, err := url.Parse(cfg.APIURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return cfg, fmt.Errorf("solver config: apiUrl %q must be an https:// URL", cfg.APIURL)
	}
	return cfg, nil
}

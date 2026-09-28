package main

import (
	"errors"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"k8s.io/client-go/rest"
)

// freensSolver presents and cleans up ACME DNS-01 TXT records in FreeNS.
type freensSolver struct{}

var _ webhook.Solver = (*freensSolver)(nil)

var errNotImplemented = errors.New("not implemented")

// Name is the solverName referenced in the Issuer's webhook stanza.
func (s *freensSolver) Name() string {
	return "freens"
}

func (s *freensSolver) Present(_ *v1alpha1.ChallengeRequest) error {
	return errNotImplemented
}

func (s *freensSolver) CleanUp(_ *v1alpha1.ChallengeRequest) error {
	return errNotImplemented
}

func (s *freensSolver) Initialize(_ *rest.Config, _ <-chan struct{}) error {
	return nil
}

package main

import (
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
)

// DefaultGroupName is the API group the webhook registers under when
// GROUP_NAME is not set. It must match `groupName` in the Issuer's webhook
// solver stanza.
const DefaultGroupName = "acme.freens.ru"

func groupName() string {
	if g := os.Getenv("GROUP_NAME"); g != "" {
		return g
	}
	return DefaultGroupName
}

// main only starts the server; excluded from the unit coverage gate and
// exercised by `make smoke` (`webhook --help` in the built image, ci and release).
func main() {
	cmd.RunWebhookServer(groupName(), &freensSolver{})
}

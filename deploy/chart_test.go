// Package deploy holds render tests for the Helm chart; `helm` (pinned in
// mise.toml) must be on PATH.
package deploy

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

const chartDir = "./cert-manager-webhook-freens"

type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	raw []byte
}

func render(t *testing.T, args ...string) []object {
	t.Helper()
	cmd := exec.Command("helm", append([]string{"template", "rel", chartDir, "--namespace", "cert-manager"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, stderr.String())
	}
	var objs []object
	for _, doc := range strings.Split(string(out), "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var o object
		if err := yaml.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatalf("decode rendered doc: %v\n%s", err, doc)
		}
		if o.Kind == "" {
			continue
		}
		o.raw = []byte(doc)
		objs = append(objs, o)
	}
	return objs
}

func decode[T any](t *testing.T, o object) T {
	t.Helper()
	var v T
	if err := yaml.Unmarshal(o.raw, &v); err != nil {
		t.Fatalf("decode %s %s: %v", o.Kind, o.Metadata.Name, err)
	}
	return v
}

func byKind(objs []object, kind string) []object {
	var out []object
	for _, o := range objs {
		if o.Kind == kind {
			out = append(out, o)
		}
	}
	return out
}

func deployment(t *testing.T, objs []object) appsv1.Deployment {
	t.Helper()
	ds := byKind(objs, "Deployment")
	if len(ds) != 1 {
		t.Fatalf("Deployments = %d, want 1", len(ds))
	}
	return decode[appsv1.Deployment](t, ds[0])
}

func touchesSecrets(r rbacv1.PolicyRule) bool {
	return (slices.Contains(r.APIGroups, "") || slices.Contains(r.APIGroups, "*")) && (slices.Contains(r.Resources, "secrets") || slices.Contains(r.Resources, "*"))
}

func TestClusterRolesGrantNoSecrets(t *testing.T) {
	for _, o := range byKind(render(t), "ClusterRole") {
		for _, r := range decode[rbacv1.ClusterRole](t, o).Rules {
			if touchesSecrets(r) {
				t.Errorf("ClusterRole %s grants Secrets: %+v", o.Metadata.Name, r)
			}
		}
	}
}

func TestSecretAccessIsGetByNameInCertManagerNamespace(t *testing.T) {
	var rules []rbacv1.PolicyRule
	for _, o := range byKind(render(t), "Role") {
		role := decode[rbacv1.Role](t, o)
		for _, r := range role.Rules {
			if !touchesSecrets(r) {
				continue
			}
			if role.Namespace != "cert-manager" {
				t.Errorf("Role %s/%s grants Secrets outside cert-manager", role.Namespace, role.Name)
			}
			rules = append(rules, r)
		}
	}
	if len(rules) != 1 {
		t.Fatalf("Secret rules = %+v, want exactly one", rules)
	}
	r := rules[0]
	if !slices.Equal(r.Verbs, []string{"get"}) {
		t.Errorf("Secret verbs = %v, want [get]", r.Verbs)
	}
	if !slices.Equal(r.ResourceNames, []string{"freens-api-key"}) {
		t.Errorf("Secret resourceNames = %v, want [freens-api-key]", r.ResourceNames)
	}
}

func TestSecretNameFromValues(t *testing.T) {
	for _, o := range byKind(render(t, "--set", "apiKeySecret.name=other-key"), "Role") {
		for _, r := range decode[rbacv1.Role](t, o).Rules {
			if touchesSecrets(r) && !slices.Equal(r.ResourceNames, []string{"other-key"}) {
				t.Errorf("resourceNames = %v, want [other-key]", r.ResourceNames)
			}
		}
	}
}

func TestImageDefaultsToAppVersion(t *testing.T) {
	c := deployment(t, render(t)).Spec.Template.Spec.Containers[0]
	if c.Image != "docker.io/hubbitus/cert-manager-webhook-freens:0.1.0" {
		t.Fatalf("image = %q", c.Image)
	}
}

func TestImageDigestPinsImage(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := deployment(t, render(t, "--set", "image.digest="+digest)).Spec.Template.Spec.Containers[0]
	if c.Image != "docker.io/hubbitus/cert-manager-webhook-freens:0.1.0@"+digest {
		t.Fatalf("image = %q", c.Image)
	}
}

func TestDeploymentRunsUnprivileged(t *testing.T) {
	d := deployment(t, render(t))
	c := d.Spec.Template.Spec.Containers[0]

	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	if env["GROUP_NAME"] != "acme.freens.ru" {
		t.Errorf("GROUP_NAME = %q, want acme.freens.ru", env["GROUP_NAME"])
	}
	if !slices.Contains(c.Args, "--secure-port=8443") || c.Ports[0].ContainerPort != 8443 {
		t.Errorf("args %v / port %d: want unprivileged port 8443", c.Args, c.Ports[0].ContainerPort)
	}
	sc := c.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot ||
		sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.Capabilities == nil || !slices.Contains(sc.Capabilities.Drop, "ALL") {
		t.Errorf("container securityContext = %+v, want non-root, read-only rootfs, no escalation, drop ALL", sc)
	}
}

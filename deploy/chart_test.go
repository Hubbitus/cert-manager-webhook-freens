// Package deploy holds render tests for the Helm chart; `helm` (pinned in
// mise.toml) must be on PATH.
package deploy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	crs := byKind(render(t), "ClusterRole")
	if len(crs) == 0 {
		t.Fatal("no ClusterRole rendered; the check below would pass vacuously")
	}
	for _, o := range crs {
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
	var names [][]string
	for _, o := range byKind(render(t, "--set", "apiKeySecret.name=other-key"), "Role") {
		for _, r := range decode[rbacv1.Role](t, o).Rules {
			if touchesSecrets(r) {
				names = append(names, r.ResourceNames)
			}
		}
	}
	if len(names) != 1 || !slices.Equal(names[0], []string{"other-key"}) {
		t.Fatalf("Secret rule resourceNames = %v, want exactly one rule with [other-key]", names)
	}
}

const (
	chartSA     = "rel-cert-manager-webhook-freens"
	chartPrefix = chartSA + ":"
)

type binding struct {
	roleKind, roleName string
	subjects           []rbacv1.Subject
}

func sa(ns, name string) rbacv1.Subject {
	return rbacv1.Subject{Kind: "ServiceAccount", Name: name, Namespace: ns}
}

// subjects drops apiGroup, which the chart renders as "".
func subjects(ss []rbacv1.Subject) []rbacv1.Subject {
	out := make([]rbacv1.Subject, 0, len(ss))
	for _, s := range ss {
		out = append(out, rbacv1.Subject{Kind: s.Kind, Name: s.Name, Namespace: s.Namespace})
	}
	return out
}

// Every binding the chart renders, keyed by kind/namespace/name. Together they
// are the effective permissions of the webhook SA (and what cert-manager gets).
func TestBindingsAreExactlyTheIntendedOnes(t *testing.T) {
	webhook := sa("cert-manager", chartSA)
	want := map[string]binding{
		"ClusterRoleBinding//" + chartPrefix + "auth-delegator":                    {"ClusterRole", "system:auth-delegator", []rbacv1.Subject{webhook}},
		"ClusterRoleBinding//" + chartPrefix + "domain-solver":                     {"ClusterRole", chartPrefix + "domain-solver", []rbacv1.Subject{sa("cert-manager", "cert-manager")}},
		"RoleBinding/kube-system/" + chartPrefix + "webhook-authentication-reader": {"Role", "extension-apiserver-authentication-reader", []rbacv1.Subject{webhook}},
		"RoleBinding/cert-manager/" + chartPrefix + "secret-reader":                {"Role", chartPrefix + "secret-reader", []rbacv1.Subject{webhook}},
	}

	objs := render(t)
	got := map[string]binding{}
	for _, o := range byKind(objs, "ClusterRoleBinding") {
		b := decode[rbacv1.ClusterRoleBinding](t, o)
		got["ClusterRoleBinding//"+b.Name] = binding{b.RoleRef.Kind, b.RoleRef.Name, subjects(b.Subjects)}
	}
	for _, o := range byKind(objs, "RoleBinding") {
		b := decode[rbacv1.RoleBinding](t, o)
		got["RoleBinding/"+b.Namespace+"/"+b.Name] = binding{b.RoleRef.Kind, b.RoleRef.Name, subjects(b.Subjects)}
	}

	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if g.roleKind != w.roleKind || g.roleName != w.roleName || !slices.Equal(g.subjects, w.subjects) {
			t.Errorf("%s = %+v, want %+v", k, g, w)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected binding %s = %+v", k, got[k])
		}
	}

	if d := deployment(t, objs); d.Spec.Template.Spec.ServiceAccountName != chartSA {
		t.Errorf("Deployment serviceAccountName = %q, want %q", d.Spec.Template.Spec.ServiceAccountName, chartSA)
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

func TestDefaultResources(t *testing.T) {
	r := deployment(t, render(t)).Spec.Template.Spec.Containers[0].Resources
	want := map[string]string{
		"requests.cpu":    "10m",
		"requests.memory": "32Mi",
		"limits.memory":   "64Mi",
	}
	got := map[string]string{
		"requests.cpu":    r.Requests.Cpu().String(),
		"requests.memory": r.Requests.Memory().String(),
		"limits.memory":   r.Limits.Memory().String(),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	if _, ok := r.Limits["cpu"]; ok {
		t.Errorf("CPU limit set (%s); only memory is limited", r.Limits.Cpu())
	}
}

// helm push stores the chart in <namespace>/<chart name>. If that equals the
// image repository, chart and image share one tag and the later push
// retags the other.
func TestChartRepoDiffersFromImageRepo(t *testing.T) {
	var chart struct {
		Name string `json:"name"`
	}
	var values struct {
		Image struct {
			Repository string `json:"repository"`
		} `json:"image"`
	}
	for file, v := range map[string]any{"Chart.yaml": &chart, "values.yaml": &values} {
		data, err := os.ReadFile(filepath.Join(chartDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, v); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	image := values.Image.Repository[strings.LastIndex(values.Image.Repository, "/")+1:]
	if chart.Name == "" || image == "" || chart.Name == image {
		t.Fatalf("chart name %q vs image repository %q: must be non-empty and differ", chart.Name, values.Image.Repository)
	}
}

// cert-manager may only create challenge payloads in the solver's API group.
func TestDomainSolverRulesExact(t *testing.T) {
	var found bool
	for _, o := range byKind(render(t), "ClusterRole") {
		if o.Metadata.Name != chartPrefix+"domain-solver" {
			continue
		}
		found = true
		rules := decode[rbacv1.ClusterRole](t, o).Rules
		want := rbacv1.PolicyRule{APIGroups: []string{"acme.freens.ru"}, Resources: []string{"*"}, Verbs: []string{"create"}}
		if len(rules) != 1 || !slices.Equal(rules[0].APIGroups, want.APIGroups) ||
			!slices.Equal(rules[0].Resources, want.Resources) || !slices.Equal(rules[0].Verbs, want.Verbs) ||
			len(rules[0].ResourceNames) != 0 || len(rules[0].NonResourceURLs) != 0 {
			t.Fatalf("domain-solver rules = %+v, want exactly [%+v]", rules, want)
		}
	}
	if !found {
		t.Fatal("domain-solver ClusterRole not rendered")
	}
}

// A role nobody binds is dead weight at best and a trap for a later binding.
func TestEveryRoleIsBound(t *testing.T) {
	objs := render(t)
	bound := map[string]bool{}
	for _, o := range byKind(objs, "ClusterRoleBinding") {
		b := decode[rbacv1.ClusterRoleBinding](t, o)
		bound[b.RoleRef.Kind+"//"+b.RoleRef.Name] = true
	}
	for _, o := range byKind(objs, "RoleBinding") {
		b := decode[rbacv1.RoleBinding](t, o)
		if b.RoleRef.Kind == "ClusterRole" {
			bound["ClusterRole//"+b.RoleRef.Name] = true
		} else {
			bound["Role/"+b.Namespace+"/"+b.RoleRef.Name] = true
		}
	}
	roles := 0
	for _, o := range byKind(objs, "ClusterRole") {
		roles++
		if !bound["ClusterRole//"+o.Metadata.Name] {
			t.Errorf("ClusterRole %s is not bound", o.Metadata.Name)
		}
	}
	for _, o := range byKind(objs, "Role") {
		roles++
		if k := "Role/" + o.Metadata.Namespace + "/" + o.Metadata.Name; !bound[k] {
			t.Errorf("Role %s is not bound", k)
		}
	}
	if roles == 0 {
		t.Fatal("no Role or ClusterRole rendered")
	}
}

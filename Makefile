# Tool versions are pinned in mise.toml; run targets inside `mise exec --` or an
# activated mise shell.
GO ?= go

.PHONY: all
all: vet lint test build

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -o cert-manager-webhook-freens .

.PHONY: vet
vet:
	$(GO) vet -tags conformance ./...

.PHONY: lint
lint:
	golangci-lint run --build-tags conformance ./...

# Unit tests only; the conformance suite against live FreeNS is `test-conformance`.
.PHONY: test
test:
	$(GO) test -race ./...

# envtest control plane for the conformance suite; setup-envtest is pinned in mise.toml.
ENVTEST_K8S_VERSION := 1.37.0
ENVTEST_DIR := $(CURDIR)/bin/k8s/$(ENVTEST_K8S_VERSION)-$(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH)

.PHONY: setup-envtest
setup-envtest:
	setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(CURDIR)/bin -p path

# cert-manager conformance suite against the live FreeNS API (ADR-0076).
# Needs FREENS_API_KEY; the key only reaches a git-ignored file that is removed
# after the run, never argv or output.
.PHONY: test-conformance
test-conformance:
	@if [ -z "$$FREENS_API_KEY" ]; then \
		echo "SKIP test-conformance: FREENS_API_KEY is not set"; \
		exit 0; \
	fi; \
	set -e; \
	$(MAKE) --no-print-directory setup-envtest >/dev/null; \
	trap 'rm -f testdata/freens/api-key.yaml' EXIT; \
	(umask 077; envsubst '$$FREENS_API_KEY' < testdata/freens/api-key.yaml.tpl > testdata/freens/api-key.yaml); \
	TEST_ASSET_ETCD=$(ENVTEST_DIR)/etcd \
	TEST_ASSET_KUBE_APISERVER=$(ENVTEST_DIR)/kube-apiserver \
	TEST_ASSET_KUBECTL=$(ENVTEST_DIR)/kubectl \
	$(GO) test -tags conformance -count=1 -v -run '^TestConformance$$' .

.PHONY: clean
clean:
	rm -rf cert-manager-webhook-freens bin _out _test

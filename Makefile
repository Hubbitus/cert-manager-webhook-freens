# Tool versions are pinned in mise.toml; run targets inside `mise exec --` or an
# activated mise shell.
GO ?= go

.PHONY: all
all: check build

# Everything CI and release gate on.
.PHONY: check
check: vet lint cover helm-lint vulncheck

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -o cert-manager-webhook-freens .

.PHONY: vet
vet:
	$(GO) vet -tags conformance ./...

.PHONY: lint
lint:
	golangci-lint run --build-tags conformance ./...

# Unit tests; same as `cover` (with the coverage gate). The conformance suite
# against live FreeNS is `test-conformance`.
.PHONY: test
test: cover

# envtest control plane for the conformance suite; setup-envtest is pinned in mise.toml.
ENVTEST_K8S_VERSION := 1.37.0
ENVTEST_DIR := $(CURDIR)/bin/k8s/$(ENVTEST_K8S_VERSION)-$(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH)

.PHONY: setup-envtest
setup-envtest:
	setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(CURDIR)/bin -p path

# cert-manager conformance suite against the live FreeNS API.
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

# Unit tests with a coverage gate: every function at 100 % except main(),
# which only starts the server and is covered by `make smoke`.
COVER_OUT := _out/cover.out

.PHONY: cover
cover:
	mkdir -p _out
	$(GO) test -race -coverprofile=$(COVER_OUT) ./...
	@$(GO) tool cover -func=$(COVER_OUT) | awk '\
		$$1 == "total:" { next } \
		$$1 ~ /\/main\.go:[0-9]+:$$/ && $$2 == "main" { next } \
		$$3 != "100.0%" { print "not fully covered: " $$0; bad = 1 } \
		END { if (bad) exit 1; print "coverage gate: all functions at 100% (main excluded)" }'

.PHONY: vulncheck
vulncheck:
	govulncheck ./...

.PHONY: helm-lint
helm-lint:
	helm lint --strict deploy/cert-manager-webhook-freens

IMAGE ?= docker.io/hubbitus/cert-manager-webhook-freens
IMAGE_TAG ?= dev
# `make smoke CONTAINER=podman` locally without docker.
CONTAINER ?= docker

.PHONY: image
image:
	$(CONTAINER) build -t $(IMAGE):$(IMAGE_TAG) .

# Container smoke run: the image starts and main() parses its flags.
.PHONY: smoke
smoke: image
	@out=$$($(CONTAINER) run --rm $(IMAGE):$(IMAGE_TAG) --help 2>&1) || { echo "$$out"; echo "smoke: --help exited non-zero"; exit 1; }; \
	echo "$$out" | grep -q '^Usage:' || { echo "$$out"; echo "smoke: no Usage: in --help output"; exit 1; }; \
	echo "smoke: ok"

.PHONY: clean
clean:
	rm -rf cert-manager-webhook-freens bin _out _test

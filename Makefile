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
	$(GO) vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

# Unit tests only; the conformance suite against live FreeNS is `test-conformance`.
.PHONY: test
test:
	$(GO) test -race ./...

.PHONY: clean
clean:
	rm -rf cert-manager-webhook-freens bin _out _test

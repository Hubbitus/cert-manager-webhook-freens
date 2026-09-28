# cert-manager-webhook-freens

[cert-manager](https://cert-manager.io/) ACME DNS-01 webhook solver for [FreeNS](https://freens.ru/) ([API](https://www.freens.ru/api-docs)).

The solver is stateless: `Present` creates the `_acme-challenge` TXT record (TTL 60) unless a TXT record with the same name and value already exists; `CleanUp` lists the zone and deletes every TXT record whose name and value match the challenge. No record id is kept in memory, so a pod restart between `Present` and `CleanUp` leaves nothing behind, and concurrent challenges for the same name (for example `example.org` and `*.example.org`) do not remove each other's records.

Scaffolded from [cert-manager/webhook-example](https://github.com/cert-manager/webhook-example) at commit `62cb1d42de165036395b4d2ed66f8c0cb2a1c1a4` (the repository has no tags).

## Installation

The image `docker.io/hubbitus/cert-manager-webhook-freens` and the Helm chart `oci://registry-1.docker.io/hubbitus/cert-manager-webhook-freens` are published on every `vX.Y.Z` tag. Install into cert-manager's namespace, pinning the chart version and the image digest:

```bash
helm install cert-manager-webhook-freens oci://registry-1.docker.io/hubbitus/cert-manager-webhook-freens \
  --version <X.Y.Z> --namespace cert-manager --set image.digest=sha256:<digest>
```

Put the FreeNS API key into a Secret in the same namespace. The chart lets the webhook `get` only that Secret name (`apiKeySecret.name`, default `freens-api-key`):

```bash
kubectl -n cert-manager create secret generic freens-api-key --from-literal=api-key=<key>
```

Reference the solver from a `ClusterIssuer`:

```yaml
solvers:
  - dns01:
      webhook:
        groupName: acme.freens.ru
        solverName: freens
        config:
          apiKeySecretRef:
            name: freens-api-key
            key: api-key
          # Optional: FreeNS domain, when it differs from the SOA-resolved zone.
          zone: example.org
          # Optional: defaults to https://freens.ru/api/v1
          apiUrl: https://freens.ru/api/v1
```

## Development

Tool versions are pinned in [`mise.toml`](mise.toml).

```bash
mise install
mise exec -- make all   # vet, lint, unit tests, build
```

### Conformance

The cert-manager conformance suite runs against the live FreeNS zone `dev.neinache.com`, under the `ci.` label, and queries the authoritative `a.freens.ru` directly. It is a separate target, not part of `go test ./...`; without `FREENS_API_KEY` it prints `SKIP` and exits 0.

```bash
FREENS_API_KEY=... mise exec -- make test-conformance
```

CI runs it on every push to `main` and on tags, with the key from the `FREENS_API_KEY` repository secret.

## Versions

Latest stable releases, checked 2026-09-28 against GitHub Releases, `go.dev/dl` and the registries.

| Component                                 | Version / pin                                                                                              |
|-------------------------------------------|------------------------------------------------------------------------------------------------------------|
| Go                                        | `1.27.1`                                                                                                   |
| `github.com/cert-manager/cert-manager`    | `v1.21.2`                                                                                                  |
| `k8s.io/client-go`                        | `v0.37.1`                                                                                                  |
| `cert-manager/webhook-example` (scaffold) | commit `62cb1d42de165036395b4d2ed66f8c0cb2a1c1a4` (no tags upstream)                                       |
| golangci-lint                             | `2.14.0`                                                                                                   |
| Helm                                      | `4.3.0`                                                                                                    |
| setup-envtest / envtest Kubernetes        | `0.25.1` / `1.37.0`                                                                                        |
| `actions/checkout`                        | `v7.0.1` @ `3d3c42e5aac5ba805825da76410c181273ba90b1`                                                      |
| `docker/setup-buildx-action`              | `v4.4.1` @ `f87e5991a6d7451dcb8d9637bfbc97413f497069`                                                      |
| `docker/login-action`                     | `v4.6.0` @ `dbcb813823bdd20940b903addbd779551569679f`                                                      |
| `docker/build-push-action`                | `v7.4.0` @ `c3c9e263c25d99ce0380d002d59b67737d91b0dc`                                                      |
| Builder image                             | `golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414`         |
| Runtime image                             | `gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3` |
| `jdx/mise-action`                         | `v4.3.0` @ `c2a87611a18de5b3828c5652fe268e992400cb5c`                                                      |

## Releasing

Set `version` and `appVersion` in `deploy/cert-manager-webhook-freens/Chart.yaml` to `X.Y.Z`, then push tag `vX.Y.Z`. The release workflow refuses a tag that differs from the chart, builds `linux/amd64` and `linux/arm64`, pushes the image and the chart, and prints the image digest in the job summary. Repository secrets: `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN` (release), `FREENS_API_KEY` (conformance).

## License

[Apache-2.0](LICENSE), as the upstream `webhook-example`.

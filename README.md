# cert-manager-webhook-freens

[cert-manager](https://cert-manager.io/) ACME DNS-01 webhook solver for [FreeNS](https://freens.ru/) ([API](https://www.freens.ru/api-docs)).

The solver is stateless: `Present` creates the `_acme-challenge` TXT record (TTL 60) unless a TXT record with the same name and value already exists; `CleanUp` lists the zone and deletes every TXT record whose name and value match the challenge. No record id is kept in memory, so a pod restart between `Present` and `CleanUp` leaves nothing behind, and concurrent challenges for the same name (for example `example.org` and `*.example.org`) do not remove each other's records.

Scaffolded from [cert-manager/webhook-example](https://github.com/cert-manager/webhook-example) at commit `62cb1d42de165036395b4d2ed66f8c0cb2a1c1a4` (the repository has no tags).

## Development

Tool versions are pinned in [`mise.toml`](mise.toml).

```bash
mise install
mise exec -- make all   # vet, lint, unit tests, build
```

## Versions

Latest stable releases, checked 2026-09-28 against GitHub Releases, `go.dev/dl` and the registries.

| Component                                 | Version / pin                                                        |
|-------------------------------------------|----------------------------------------------------------------------|
| Go                                        | `1.27.1`                                                             |
| `github.com/cert-manager/cert-manager`    | `v1.21.2`                                                            |
| `k8s.io/client-go`                        | `v0.37.1`                                                            |
| `cert-manager/webhook-example` (scaffold) | commit `62cb1d42de165036395b4d2ed66f8c0cb2a1c1a4` (no tags upstream) |
| golangci-lint                             | `2.14.0`                                                             |
| Helm                                      | `4.3.0`                                                              |
| `actions/checkout`                        | `v7.0.1` @ `3d3c42e5aac5ba805825da76410c181273ba90b1`                |
| `jdx/mise-action`                         | `v4.3.0` @ `c2a87611a18de5b3828c5652fe268e992400cb5c`                |

## License

[Apache-2.0](LICENSE), as the upstream `webhook-example`.

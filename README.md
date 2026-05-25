# mdFly

CLI-driven markdown sharing service.

```
mdfly publish foo.md
# → https://mdfly.dev/abc12345
```

## Development

```sh
make build   # bin/mdfly + bin/mdfly-server
make test    # go test ./...
make lint    # golangci-lint
```

See [CONTEXT.md](CONTEXT.md) for the authoritative spec and [docs/adr/](docs/adr/) for architecture decisions.

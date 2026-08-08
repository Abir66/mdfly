# mdFly

CLI-driven markdown sharing service.

```
mdfly publish foo.md
# → https://mdfly.dev/abc12345
```

## Install

Homebrew (macOS, Linux):

```sh
brew install Abir66/mdfly/mdfly
```

Shell installer (macOS, Linux) — verifies the download against the release checksums:

```sh
curl -fsSL https://raw.githubusercontent.com/Abir66/mdfly/main/scripts/install.sh | sh
```

Go toolchain:

```sh
go install github.com/Abir66/mdfly/cmd/mdfly@latest
```

Windows: download the archive from [Releases](https://github.com/Abir66/mdfly/releases).

## Development

```sh
make build   # bin/mdfly + bin/mdfly-server
make test    # go test ./...
make lint    # golangci-lint
```

See [CONTEXT.md](CONTEXT.md) for the authoritative spec and [docs/adr/](docs/adr/) for architecture decisions.

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

## Releasing

The root `VERSION` file is the version. Never write a git tag by hand.

1. Edit `VERSION` (e.g. `0.1.0` → `0.2.0`), open a PR, merge it
2. `git checkout main && git pull`
3. `make release`

`make release` reads `VERSION`, refuses unless you are on a clean `main` level with
`origin/main` and the tag is unused, then tags and pushes. Pushing the tag is what
builds and publishes the release. `./scripts/release.sh --dry-run` runs every check
and creates nothing.

See [CONTEXT.md](CONTEXT.md) for the authoritative spec and [docs/adr/](docs/adr/) for architecture decisions.

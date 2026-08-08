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
curl -fsSL https://mdfly.dev/install.sh | sh
```

Go toolchain:

```sh
go install github.com/Abir66/mdfly/cmd/mdfly@latest
```

Windows: download the `mdfly_<version>_windows_amd64.zip` archive from
[Releases](https://github.com/Abir66/mdfly/releases). Windows arm64 is not published in v1.

## Development

```sh
make build   # bin/mdfly + bin/mdfly-server
make test    # go test ./...
make lint    # golangci-lint
```

## Releasing

The root `VERSION` file is the version. Never write a git tag by hand.

1. Move your entries from `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md) into a
   `## [<version>] - <YYYY-MM-DD>` section
2. Edit `VERSION` (e.g. `0.1.0` → `0.2.0`), open a PR with both changes, merge it
3. `git checkout main && git pull`
4. `make release`

`make release` reads `VERSION`, refuses unless you are on a clean `main` level with
`origin/main`, the tag is unused, and `CHANGELOG.md` has a non-empty section for that
version — then tags and pushes. Pushing the tag is what builds and publishes the
release. `./scripts/release.sh --dry-run` runs every check and creates nothing, so it
doubles as a changelog check before you open the PR.

The GitHub Release body is that changelog section verbatim, plus the install block in
`.github/release-footer.md`. Nothing is generated from commit messages.

See [CONTEXT.md](CONTEXT.md) for the authoritative spec and [docs/adr/](docs/adr/) for architecture decisions.

---
title: Install
order: 1
---

Pick one. All three put an `mdfly` binary on your path.

### Shell installer

```sh
curl -fsSL https://mdfly.dev/install.sh | sh
```

The installer verifies the download against the release checksums.

### Homebrew

```sh
brew install Abir66/mdfly/mdfly
```

### Go

```sh
go install github.com/Abir66/mdfly/cmd/mdfly@latest
```

A binary built with `go install` reports its version as `dev`.

### Platforms

Release binaries are built for macOS (amd64, arm64), Linux (amd64, arm64) and Windows (amd64). Check the install with `mdfly --version`.

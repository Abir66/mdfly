# Changelog

Written by hand, not generated from commits. The GitHub Release for `v<X.Y.Z>` is
exactly the `## [X.Y.Z]` section below it, so an entry belongs here if a user would
change what they do because of it — not because a commit exists.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-08-08

First published release of the CLI.

### Added

- `mdfly publish <file>` uploads a file plus every local asset it references and
  prints a shareable URL. `-r` follows linked `.md` files and linked folders
  transitively; `-m` publishes inline text instead of a file, as does piped stdin.
- `mdfly update`, `delete`, `list`, `open`, and `remove` operate over a slug-keyed
  local ledger, so a document can be re-published or taken down from the machine
  that created it.
- Anonymous documents: no account needed. The CLI holds a per-slug edit token in
  mode-0600 local state, and documents expire after 30 days.
- Server-rendered viewer with a sidebar file tree, directory listings, and a
  GitHub-style view for code files; single-file bundles render without the sidebar.
- `/llm/<slug>` serves the raw markdown twin of every page for AI agents.
- Install paths: a Homebrew cask (`brew install Abir66/mdfly/mdfly`), a
  checksum-verifying `scripts/install.sh`, and prebuilt archives per platform.
- Once-per-24h update check that names the upgrade command for how you installed.

### Fixed

- Local state locks correctly on Windows, which has no `flock(2)`.
- The bundle carries file bytes captured during the reference walk instead of
  re-reading each file at upload time, so editing a file mid-publish can no longer
  produce a bundle whose contents disagree with its manifest.
- Non-regular files (sockets, devices, FIFOs) are skipped by the reference walk
  rather than aborting it.

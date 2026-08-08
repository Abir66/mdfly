#!/usr/bin/env bash
# Cuts a release by turning the VERSION file into a git tag. The tag is what the
# release workflow watches and what goreleaser stamps into the binary, so this
# script exists to keep a hand-typed `git tag` from ever being the source of
# truth — VERSION is, and the tag is derived from it.
#
#   ./scripts/release.sh [--dry-run] [-y]     (or `make release`)
#
# Every check below refuses rather than repairs: a tag is public the moment it is
# pushed, and the cheapest fix for a wrong one is to not create it.
set -Eeuo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
VERSION_FILE="$REPO_ROOT/VERSION"
RELEASE_BRANCH=main
REMOTE=origin
# Bare MAJOR.MINOR.PATCH with an optional prerelease suffix (0.2.0-rc.1). Build
# metadata (+foo) is excluded: it is legal semver but not a legal git ref.
SEMVER_RE='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

usage() {
	cat <<'EOF'
usage: release.sh [--dry-run] [-y]

Reads VERSION, verifies the repo is in a releasable state, then tags and pushes.
  --dry-run   run every check and print the tag, but create nothing
  -y          skip the confirmation prompt
EOF
}

DRY_RUN=0
ASSUME_YES=0

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--dry-run) DRY_RUN=1 ;;
		-y | --yes) ASSUME_YES=1 ;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			usage >&2
			die "unknown argument: $1"
			;;
		esac
		shift
	done

	local version tag
	version=$(read_version)
	tag="v$version"

	require_releasable_worktree
	require_synced_with_remote
	require_tag_available "$tag"

	if [ "$DRY_RUN" = 1 ]; then
		info "dry run: would tag $tag at $(git -C "$REPO_ROOT" rev-parse --short HEAD) and push to $REMOTE"
		return 0
	fi

	confirm "$tag"
	git -C "$REPO_ROOT" tag -a "$tag" -m "$tag"
	git -C "$REPO_ROOT" push "$REMOTE" "refs/tags/$tag"
	info "pushed $tag — the release workflow is now building it"
}

# Reads and validates VERSION. A leading v is tolerated so that writing "v0.2.0"
# is a typo rather than a broken tag.
read_version() {
	[ -f "$VERSION_FILE" ] || die "no VERSION file at $VERSION_FILE"

	local raw
	raw=$(tr -d '[:space:]' <"$VERSION_FILE")
	raw=${raw#v}

	[ -n "$raw" ] || die "VERSION is empty"
	[[ $raw =~ $SEMVER_RE ]] || die "VERSION is not a semantic version: '$raw' (expected e.g. 0.2.0)"

	printf '%s' "$raw"
}

require_releasable_worktree() {
	local branch
	branch=$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)
	[ "$branch" = "$RELEASE_BRANCH" ] ||
		die "on branch '$branch'; releases are cut from '$RELEASE_BRANCH'"

	[ -z "$(git -C "$REPO_ROOT" status --porcelain)" ] ||
		die "working tree is dirty; commit or stash first"
}

# A tag on a stale HEAD would ship code that is not what main holds, and the
# mistake is invisible afterwards — the tag looks fine, it just points at the
# wrong commit.
require_synced_with_remote() {
	git -C "$REPO_ROOT" fetch --quiet "$REMOTE" "$RELEASE_BRANCH" ||
		die "cannot reach $REMOTE"

	local local_head remote_head
	local_head=$(git -C "$REPO_ROOT" rev-parse HEAD)
	remote_head=$(git -C "$REPO_ROOT" rev-parse "$REMOTE/$RELEASE_BRANCH")
	[ "$local_head" = "$remote_head" ] ||
		die "HEAD is not $REMOTE/$RELEASE_BRANCH; run 'git pull' first"
}

# Checks the remote too: a tag deleted locally but still on the remote would fail
# only at push time, after the local tag already exists.
require_tag_available() {
	local tag=$1

	! git -C "$REPO_ROOT" rev-parse -q --verify "refs/tags/$tag" >/dev/null ||
		die "tag $tag already exists locally; bump VERSION"

	[ -z "$(git -C "$REPO_ROOT" ls-remote --tags "$REMOTE" "refs/tags/$tag")" ] ||
		die "tag $tag already exists on $REMOTE; bump VERSION"
}

confirm() {
	local tag=$1
	[ "$ASSUME_YES" = 1 ] && return 0

	printf 'Tag %s at %s and push to %s? [y/N] ' \
		"$tag" "$(git -C "$REPO_ROOT" rev-parse --short HEAD)" "$REMOTE" >&2
	local answer
	read -r answer
	case "$answer" in
	y | Y | yes | YES) return 0 ;;
	*) die "aborted" ;;
	esac
}

info() {
	echo "release: $*" >&2
}

die() {
	echo "release: $*" >&2
	exit 1
}

main "$@"

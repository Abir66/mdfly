#!/usr/bin/env bash
# Reads one version's section out of the hand-written CHANGELOG.md. The release
# workflow pipes this into `goreleaser release --release-notes`, which is why the
# output must be the release body and nothing else — no heading, no surrounding
# blank lines.
#
#   ./scripts/changelog.sh extract 0.2.0
#
# Exits non-zero when the section is missing or has no content, so release.sh can
# refuse to tag a version nobody wrote notes for.
set -Eeuo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
CHANGELOG_FILE="$REPO_ROOT/CHANGELOG.md"

usage() {
	cat <<'EOF'
usage: changelog.sh extract <version>

Prints the CHANGELOG.md section for <version> (bare, no leading v) to stdout.
EOF
}

main() {
	local command=${1-}
	case "$command" in
	extract)
		shift
		[ $# -eq 1 ] || {
			usage >&2
			die "extract takes exactly one version"
		}
		extract "${1#v}"
		;;
	-h | --help)
		usage
		;;
	*)
		usage >&2
		die "unknown command: ${command:-<none>}"
		;;
	esac
}

extract() {
	local version=$1
	[ -f "$CHANGELOG_FILE" ] || die "no CHANGELOG.md at $CHANGELOG_FILE"

	# The command substitution drops trailing newlines and the sed drops leading
	# blank lines, so the body is tight regardless of how the file is spaced.
	local body
	body=$(section_body "$version" | sed '/./,$!d')
	[ -n "$body" ] || die "CHANGELOG.md has no content under '## [$version]'"

	printf '%s\n' "$body"
}

# Prints every line between the `## [<version>]` heading and the next `## `
# heading. Matching is by exact string, not regex, because a version is full of
# dots and `0.2.0` would otherwise also match `0.2.0-rc.1`; the closing bracket in
# the needle is what keeps a prerelease from answering for its release.
section_body() {
	local version=$1

	awk -v want="## [$version]" '
		/^## / {
			if (in_section) exit
			if (index($0, want) == 1) {
				rest = substr($0, length(want) + 1)
				if (rest ~ /^[ \t]*(-.*)?$/) in_section = 1
			}
			next
		}
		in_section { print }
	' "$CHANGELOG_FILE"
}

die() {
	echo "changelog: $*" >&2
	exit 1
}

main "$@"

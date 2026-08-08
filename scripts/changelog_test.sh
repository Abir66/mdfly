#!/usr/bin/env bash
# Tests for changelog.sh. Each test builds a sandbox holding a copy of the script
# plus a purpose-written CHANGELOG.md, because the script locates the changelog
# relative to its own path.
#
# Run: ./scripts/changelog_test.sh   (or `make test-changelog`)
set -Eeuo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FAILURES=0
CURRENT_TEST=""

# --- harness ----------------------------------------------------------------

setup() {
	CURRENT_TEST=$1
	SANDBOX=$(mktemp -d)
	mkdir -p "$SANDBOX/scripts"
	cp "$REPO_ROOT/scripts/changelog.sh" "$SANDBOX/scripts/changelog.sh"
}

teardown() {
	rm -rf "$SANDBOX"
}

write_changelog() {
	cat >"$SANDBOX/CHANGELOG.md"
}

run_extract() {
	set +e
	OUTPUT=$("$SANDBOX/scripts/changelog.sh" extract "$@" 2>&1)
	STATUS=$?
	set -e
}

fail() {
	echo "  FAIL: $CURRENT_TEST: $1" >&2
	FAILURES=$((FAILURES + 1))
}

assert_status() { [ "$STATUS" = "$1" ] || fail "expected exit $1, got $STATUS -- $OUTPUT"; }
assert_output_has() { [[ "$OUTPUT" == *"$1"* ]] || fail "expected output to contain '$1' -- $OUTPUT"; }
assert_output_lacks() { [[ "$OUTPUT" != *"$1"* ]] || fail "expected output to omit '$1' -- $OUTPUT"; }
assert_output_is() { [ "$OUTPUT" = "$1" ] || fail "expected output '$1', got '$OUTPUT'"; }

# --- tests ------------------------------------------------------------------

test_extracts_only_the_requested_section() {
	setup extracts_only_the_requested_section
	write_changelog <<'EOF'
# Changelog

## [Unreleased]

- unreleased work

## [0.3.0] - 2026-09-01

### Added
- newer thing

## [0.2.0] - 2026-08-08

### Added
- older thing
EOF

	run_extract 0.3.0
	assert_status 0
	assert_output_has "newer thing"
	assert_output_lacks "older thing"
	assert_output_lacks "unreleased work"
	assert_output_lacks "## ["
	teardown
}

# The body is pasted straight into a Release, so stray blank lines top and bottom
# are the script's job to remove, not the writer's.
test_strips_surrounding_blank_lines() {
	setup strips_surrounding_blank_lines
	write_changelog <<'EOF'
## [0.2.0] - 2026-08-08


### Added
- a thing


## [0.1.0] - 2026-07-01
EOF

	run_extract 0.2.0
	assert_status 0
	assert_output_is "### Added
- a thing"
	teardown
}

test_keeps_interior_blank_lines() {
	setup keeps_interior_blank_lines
	write_changelog <<'EOF'
## [0.2.0]

### Added
- a thing

### Fixed
- another thing
EOF

	run_extract 0.2.0
	assert_status 0
	assert_output_is "### Added
- a thing

### Fixed
- another thing"
	teardown
}

test_accepts_heading_without_a_date() {
	setup accepts_heading_without_a_date
	write_changelog <<'EOF'
## [0.2.0]
- shipped
EOF

	run_extract 0.2.0
	assert_status 0
	assert_output_is "- shipped"
	teardown
}

test_tolerates_leading_v() {
	setup tolerates_leading_v
	write_changelog <<'EOF'
## [0.2.0] - 2026-08-08
- shipped
EOF

	run_extract v0.2.0
	assert_status 0
	assert_output_is "- shipped"
	teardown
}

test_extracts_a_prerelease_section() {
	setup extracts_a_prerelease_section
	write_changelog <<'EOF'
## [0.2.0-rc.1] - 2026-08-01
- candidate
EOF

	run_extract 0.2.0-rc.1
	assert_status 0
	assert_output_is "- candidate"
	teardown
}

# A regex built from the version would let 0.2.0 match the rc heading, publishing
# the candidate's notes as the release's.
test_release_does_not_match_its_prerelease() {
	setup release_does_not_match_its_prerelease
	write_changelog <<'EOF'
## [0.2.0-rc.1] - 2026-08-01
- candidate
EOF

	run_extract 0.2.0
	assert_status 1
	assert_output_has "no content under '## [0.2.0]'"
	teardown
}

test_rejects_missing_section() {
	setup rejects_missing_section
	write_changelog <<'EOF'
## [Unreleased]

## [0.1.0] - 2026-07-01
- old
EOF

	run_extract 0.2.0
	assert_status 1
	assert_output_has "no content under '## [0.2.0]'"
	teardown
}

# The failure this whole check exists for: the writer bumped VERSION, added the
# heading, and never came back to fill it in.
test_rejects_empty_section() {
	setup rejects_empty_section
	write_changelog <<'EOF'
## [0.2.0] - 2026-08-08

## [0.1.0] - 2026-07-01
- old
EOF

	run_extract 0.2.0
	assert_status 1
	assert_output_has "no content under '## [0.2.0]'"
	teardown
}

test_rejects_missing_changelog_file() {
	setup rejects_missing_changelog_file
	run_extract 0.2.0
	assert_status 1
	assert_output_has "no CHANGELOG.md"
	teardown
}

test_rejects_unknown_command() {
	setup rejects_unknown_command
	write_changelog <<'EOF'
## [0.2.0]
- shipped
EOF

	set +e
	OUTPUT=$("$SANDBOX/scripts/changelog.sh" publish 0.2.0 2>&1)
	STATUS=$?
	set -e
	assert_status 1
	assert_output_has "unknown command: publish"
	teardown
}

test_rejects_extract_without_a_version() {
	setup rejects_extract_without_a_version
	write_changelog <<'EOF'
## [0.2.0]
- shipped
EOF

	set +e
	OUTPUT=$("$SANDBOX/scripts/changelog.sh" extract 2>&1)
	STATUS=$?
	set -e
	assert_status 1
	assert_output_has "exactly one version"
	teardown
}

# --- runner -----------------------------------------------------------------

for t in $(declare -F | awk '{print $3}' | grep '^test_'); do
	echo "== $t"
	"$t"
done

if [ "$FAILURES" -gt 0 ]; then
	echo "$FAILURES test(s) failed" >&2
	exit 1
fi
echo "all changelog.sh tests passed"

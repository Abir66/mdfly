#!/usr/bin/env bash
# Tests for release.sh. Unlike deploy_test.sh these use real git against a real
# bare repo standing in for the remote: git is the whole subject here, so stubbing
# it would test the stubs. Nothing touches the working tree or any network.
#
# Run: ./scripts/release_test.sh   (or `make test-release`)
set -Eeuo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FAILURES=0
CURRENT_TEST=""

# --- harness ----------------------------------------------------------------

# Builds a sandbox holding a clone of a bare "origin" plus the real script, so
# every check (fetch, ls-remote, push) exercises genuine git plumbing.
setup() {
	CURRENT_TEST=$1
	SANDBOX=$(mktemp -d)
	ORIGIN="$SANDBOX/origin.git"
	WORK="$SANDBOX/work"

	git init --quiet --bare --initial-branch=main "$ORIGIN"
	git clone --quiet "$ORIGIN" "$WORK" 2>/dev/null
	git -C "$WORK" config user.email test@example.com
	git -C "$WORK" config user.name Test
	git -C "$WORK" checkout --quiet -b main 2>/dev/null || true

	mkdir -p "$WORK/scripts"
	cp "$REPO_ROOT/scripts/release.sh" "$WORK/scripts/release.sh"
	echo "0.1.0" >"$WORK/VERSION"
	git -C "$WORK" add -A
	git -C "$WORK" commit --quiet -m "initial"
	git -C "$WORK" push --quiet -u origin main
}

teardown() {
	rm -rf "$SANDBOX"
}

# Runs the script under test, capturing status and merged output.
run_release() {
	set +e
	OUTPUT=$(cd "$WORK" && ./scripts/release.sh "$@" 2>&1)
	STATUS=$?
	set -e
}

fail() {
	echo "  FAIL: $CURRENT_TEST: $1" >&2
	FAILURES=$((FAILURES + 1))
}

assert_status() { [ "$STATUS" = "$1" ] || fail "expected exit $1, got $STATUS -- $OUTPUT"; }
assert_output_has() { [[ "$OUTPUT" == *"$1"* ]] || fail "expected output to contain '$1' -- $OUTPUT"; }

assert_tag_pushed() {
	git -C "$ORIGIN" rev-parse -q --verify "refs/tags/$1" >/dev/null ||
		fail "expected $1 on the remote"
}

assert_no_tag_anywhere() {
	! git -C "$WORK" rev-parse -q --verify "refs/tags/$1" >/dev/null ||
		fail "expected no local tag $1"
	! git -C "$ORIGIN" rev-parse -q --verify "refs/tags/$1" >/dev/null ||
		fail "expected no remote tag $1"
}

# --- tests ------------------------------------------------------------------

test_tags_and_pushes_version_from_file() {
	setup tags_and_pushes_version_from_file
	echo "0.4.2" >"$WORK/VERSION"
	git -C "$WORK" commit --quiet -am "bump"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 0
	assert_output_has "pushed v0.4.2"
	assert_tag_pushed v0.4.2
	teardown
}

test_dry_run_creates_nothing() {
	setup dry_run_creates_nothing
	run_release --dry-run
	assert_status 0
	assert_output_has "would tag v0.1.0"
	assert_no_tag_anywhere v0.1.0
	teardown
}

test_leading_v_is_tolerated() {
	setup leading_v_is_tolerated
	echo "v0.2.0" >"$WORK/VERSION"
	git -C "$WORK" commit --quiet -am "bump"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 0
	assert_tag_pushed v0.2.0
	teardown
}

test_rejects_non_semver() {
	setup rejects_non_semver
	echo "abcd" >"$WORK/VERSION"
	git -C "$WORK" commit --quiet -am "bad"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 1
	assert_output_has "not a semantic version"
	teardown
}

# Shapes a loose "digits and dots" regex would wave through, each of which sorts
# wrong or not at all under a real semver comparator.
test_rejects_malformed_semver() {
	for bad in 01.2.3 1.2.3-01 1.2.3-alpha..1; do
		setup "rejects_malformed_semver:$bad"
		echo "$bad" >"$WORK/VERSION"
		git -C "$WORK" commit --quiet -am "bad"
		git -C "$WORK" push --quiet origin main

		run_release -y
		assert_status 1
		assert_output_has "not a semantic version"
		teardown
	done
}

test_accepts_prerelease() {
	setup accepts_prerelease
	echo "0.2.0-rc.1" >"$WORK/VERSION"
	git -C "$WORK" commit --quiet -am "bump"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 0
	assert_tag_pushed v0.2.0-rc.1
	teardown
}

test_rejects_empty_version() {
	setup rejects_empty_version
	: >"$WORK/VERSION"
	git -C "$WORK" commit --quiet -am "empty"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 1
	assert_output_has "VERSION is empty"
	teardown
}

test_rejects_dirty_worktree() {
	setup rejects_dirty_worktree
	echo "stray" >"$WORK/untracked.txt"
	git -C "$WORK" add untracked.txt

	run_release -y
	assert_status 1
	assert_output_has "working tree is dirty"
	assert_no_tag_anywhere v0.1.0
	teardown
}

test_rejects_non_release_branch() {
	setup rejects_non_release_branch
	git -C "$WORK" checkout --quiet -b feature

	run_release -y
	assert_status 1
	assert_output_has "releases are cut from 'main'"
	teardown
}

# The dangerous case: the tag would look correct but point at a commit that is not
# what main holds.
test_rejects_stale_head() {
	setup rejects_stale_head
	local other="$SANDBOX/other"
	git clone --quiet "$ORIGIN" "$other"
	git -C "$other" config user.email other@example.com
	git -C "$other" config user.name Other
	echo "moved on" >"$other/newfile.txt"
	git -C "$other" add -A
	git -C "$other" commit --quiet -m "someone else's merge"
	git -C "$other" push --quiet origin main

	run_release -y
	assert_status 1
	assert_output_has "run 'git pull' first"
	assert_no_tag_anywhere v0.1.0
	teardown
}

test_rejects_existing_local_tag() {
	setup rejects_existing_local_tag
	git -C "$WORK" tag v0.1.0

	run_release -y
	assert_status 1
	assert_output_has "already exists locally"
	teardown
}

test_rejects_existing_remote_tag() {
	setup rejects_existing_remote_tag
	git -C "$WORK" tag v0.1.0
	git -C "$WORK" push --quiet origin refs/tags/v0.1.0
	git -C "$WORK" tag -d v0.1.0 >/dev/null

	run_release -y
	assert_status 1
	assert_output_has "already exists on origin"
	teardown
}

test_rejects_missing_version_file() {
	setup rejects_missing_version_file
	git -C "$WORK" rm --quiet VERSION
	git -C "$WORK" commit --quiet -m "drop VERSION"
	git -C "$WORK" push --quiet origin main

	run_release -y
	assert_status 1
	assert_output_has "no VERSION file"
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
echo "all release.sh tests passed"

#!/usr/bin/env bash
# Tests for deploy.sh. Every external command it calls — docker, git — is replaced
# by a stub on PATH that records its argv and answers from fixture variables, so a
# whole deploy can be exercised with no daemon, no registry and no box.
#
# Run: ./deploy/deploy_test.sh   (or `make test-deploy`)
set -Eeuo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FAILURES=0
CURRENT_TEST=""

# --- harness ----------------------------------------------------------------

# Builds a throwaway repo whose deploy/ holds the real script, so the files the
# script writes land in the sandbox instead of the working tree.
setup() {
	CURRENT_TEST=$1
	SANDBOX=$(mktemp -d)
	mkdir -p "$SANDBOX/deploy" "$SANDBOX/bin"
	cp "$REPO_ROOT/deploy/deploy.sh" "$SANDBOX/deploy/deploy.sh"
	ln -s "$REPO_ROOT/migrations" "$SANDBOX/migrations"
	write_stubs
	STUB_LOG="$SANDBOX/calls.log"
	: >"$STUB_LOG"
	PATH="$SANDBOX/bin:$PATH"
	export STUB_LOG PATH

	STUB_HEAD_SHA=newsha
	STUB_RUNNING_SLOTS=""
	STUB_APPLIED_VERSION=3
	STUB_HEALTH_OK=1
	STUB_IMAGE_app_blue=""
	STUB_IMAGE_app_green=""
	STUB_LOCAL_TAGS=""
	export STUB_HEAD_SHA STUB_RUNNING_SLOTS STUB_APPLIED_VERSION STUB_HEALTH_OK \
		STUB_IMAGE_app_blue STUB_IMAGE_app_green STUB_LOCAL_TAGS
	export MDFLY_HEALTH_TIMEOUT=1
}

bookmark_fixture() {
	cat >"$SANDBOX/deploy/.deploy-state" <<EOF
PREVIOUS_TAG=$1
CROSSED_MIGRATION=$2
EOF
}

teardown() { rm -rf "$SANDBOX"; }

write_stubs() {
	cat >"$SANDBOX/bin/docker" <<'STUB'
#!/usr/bin/env bash
echo "docker $*" >>"$STUB_LOG"
service_filter() {
	for arg in "$@"; do
		case "$arg" in *com.docker.compose.service=*) echo "${arg##*=}" ;; esac
	done
}
if [ "$1" = "compose" ]; then
	for arg in "$@"; do
		case "$arg" in
		run) exec_migrate=1 ;;
		exec) health=1 ;;
		esac
	done
	if [ -n "${exec_migrate:-}" ]; then
		case "$*" in *version*) reading_version=1 ;; esac
		if [ "$STUB_APPLIED_VERSION" = "none" ]; then
			# Only the read reports "no migration"; an `up` against an empty
			# schema succeeds, which is the whole point of a first deploy.
			[ -z "${reading_version:-}" ] && exit 0
			echo "error: no migration" >&2
			exit 1
		fi
		if [ "$STUB_APPLIED_VERSION" = "unreachable" ]; then
			echo 'error: dial postgres://mdfly:hunter2@db.example:5432/mdfly' >&2
			exit 1
		fi
		if [ "$STUB_APPLIED_VERSION" = "migrate-fails" ] && [ -n "${reading_version:-}" ]; then
			echo "1" >&2
			exit 0
		fi
		if [ "$STUB_APPLIED_VERSION" = "migrate-fails" ]; then
			echo 'error: syntax error at or near "FOO" (postgres://mdfly:hunter2@db:5432/mdfly)' >&2
			exit 1
		fi
		echo "$STUB_APPLIED_VERSION" >&2
		exit 0
	fi
	if [ -n "${health:-}" ]; then
		[ "$STUB_HEALTH_OK" = "1" ] && exit 0
		exit 1
	fi
	exit 0
fi
case "$1" in
ps)
	svc=$(service_filter "$@")
	case " $STUB_RUNNING_SLOTS " in *" $svc "*) echo "cid-$svc" ;; esac
	;;
inspect)
	for arg in "$@"; do
		case "$arg" in cid-*) slot="${arg#cid-}" ;; esac
	done
	var="STUB_IMAGE_$slot"
	echo "${!var}"
	;;
images) printf '%s\n' $STUB_LOCAL_TAGS ;;
esac
exit 0
STUB
	cat >"$SANDBOX/bin/git" <<'STUB'
#!/usr/bin/env bash
echo "git $*" >>"$STUB_LOG"
for arg in "$@"; do
	[ "$arg" = "rev-parse" ] && { echo "$STUB_HEAD_SHA"; exit 0; }
done
exit 0
STUB
	chmod +x "$SANDBOX/bin/docker" "$SANDBOX/bin/git"
}

run_deploy() {
	set +e
	OUTPUT=$("$SANDBOX/deploy/deploy.sh" "$@" 2>&1)
	STATUS=$?
	set -e
}

fail() {
	printf '  FAIL %s: %s\n' "$CURRENT_TEST" "$1" >&2
	printf '    output: %s\n' "${OUTPUT//$'\n'/ | }" >&2
	FAILURES=$((FAILURES + 1))
}

assert_output_has() { [[ "$OUTPUT" == *"$1"* ]] || fail "expected output to contain '$1'"; }
assert_output_lacks() { [[ "$OUTPUT" != *"$1"* ]] || fail "expected output NOT to contain '$1'"; }
assert_status() { [ "$STATUS" = "$1" ] || fail "expected exit $1, got $STATUS"; }
assert_called() { grep -qF -- "$1" "$STUB_LOG" || fail "expected a call matching '$1'"; }
assert_called_exactly() { grep -qxF -- "$1" "$STUB_LOG" || fail "expected the exact call '$1'"; }
assert_not_called() { grep -qF -- "$1" "$STUB_LOG" && fail "expected NO call matching '$1'"; return 0; }

# Asserts the first line matching $1 comes before the first matching $2.
assert_call_order() {
	local first second
	first=$(grep -nF -- "$1" "$STUB_LOG" | head -1 | cut -d: -f1)
	second=$(grep -nF -- "$2" "$STUB_LOG" | head -1 | cut -d: -f1)
	if [ -z "$first" ] || [ -z "$second" ] || [ "$first" -ge "$second" ]; then
		fail "expected '$1' before '$2'"
	fi
}

# --- tests ------------------------------------------------------------------

test_dry_run_code_only() {
	setup "dry-run detects a code-only deploy"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	run_deploy --dry-run

	assert_status 0
	assert_output_has "code-only"
	assert_output_has "oldsha"
	assert_output_has "newsha"
	assert_output_has "app_green"
	assert_not_called "docker pull"
	assert_not_called "compose up"
	assert_not_called "compose stop"
	assert_not_called "pull --ff-only"
	teardown
}

test_dry_run_schema_change() {
	setup "dry-run detects a schema change"
	STUB_RUNNING_SLOTS="app_green"
	STUB_IMAGE_app_green="ghcr.io/abir66/mdfly:oldsha"
	STUB_APPLIED_VERSION=2
	run_deploy --dry-run

	assert_status 0
	assert_output_has "schema change"
	assert_output_has "app_green"
	assert_not_called "compose up"
	teardown
}

test_dry_run_cold_start() {
	setup "dry-run reports a cold start when no slot serves"
	run_deploy --dry-run

	assert_status 0
	assert_output_has "cold start"
	assert_output_has "app_blue"
	teardown
}

test_cold_box_with_pending_migrations_starts_the_whole_stack() {
	setup "a cold box with a pending migration migrates, then starts everything"
	STUB_APPLIED_VERSION=none
	run_deploy

	assert_status 0
	assert_output_has "cold start"
	# shellcheck disable=SC2016  # matching the literal command line, not expanding it
	assert_call_order '-database="$DATABASE_URL" up' "docker compose up --detach"
	# The whole default profile, not one slot — Caddy is in it and nothing else
	# would start it.
	assert_called_exactly "docker compose up --detach"
	assert_not_called "--force-recreate"
	teardown
}

test_both_slots_running_aborts() {
	setup "a deploy with both slots up refuses to guess"
	STUB_RUNNING_SLOTS="app_blue app_green"
	run_deploy --dry-run

	assert_status 1
	assert_output_has "both"
	teardown
}

test_unreadable_schema_version_hides_the_dsn() {
	setup "an unreachable database never prints its DSN"
	STUB_APPLIED_VERSION=unreachable
	run_deploy --dry-run

	assert_status 1
	assert_output_lacks "hunter2"
	assert_output_lacks "postgres://"
	teardown
}

test_swap_starts_idle_then_stops_live() {
	setup "a code-only deploy overlaps the slots"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	run_deploy

	assert_status 0
	assert_called "git -C $SANDBOX pull --ff-only"
	assert_called "docker pull ghcr.io/abir66/mdfly:newsha"
	assert_call_order "docker pull" "compose up"
	assert_call_order "compose --profile green up" "compose stop app_blue"
	assert_called "compose up --detach jobs"
	assert_called "docker image prune"
	grep -q "MDFLY_IMAGE=ghcr.io/abir66/mdfly:newsha" "$SANDBOX/deploy/.env" ||
		fail "expected the compose image file to name the new tag"
	grep -q "PREVIOUS_TAG=oldsha" "$SANDBOX/deploy/.deploy-state" ||
		fail "expected the outgoing tag to be bookmarked"
	teardown
}

test_abort_leaves_the_old_slot_serving() {
	setup "a slot that never answers /healthz aborts the deploy"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	STUB_HEALTH_OK=0
	run_deploy

	assert_status 1
	assert_called "compose --profile green stop app_green"
	assert_not_called "compose stop app_blue"
	grep -q "MDFLY_IMAGE=ghcr.io/abir66/mdfly:oldsha" "$SANDBOX/deploy/.env" ||
		fail "expected the compose image file to be restored to the serving tag"
	teardown
}

test_schema_change_migrates_before_starting_new_code() {
	setup "a schema change migrates first, then recreates in place"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	STUB_APPLIED_VERSION=2
	run_deploy

	assert_status 0
	# shellcheck disable=SC2016  # matching the literal command line, not expanding it
	assert_call_order '-database="$DATABASE_URL" up' "compose up --detach --force-recreate app_blue"
	assert_not_called "compose --profile green up"
	grep -q "CROSSED_MIGRATION=yes" "$SANDBOX/deploy/.deploy-state" ||
		fail "expected the bookmark to record the migration boundary"
	teardown
}

test_failed_migration_starts_nothing() {
	setup "a failed migration never reaches the new code"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	STUB_APPLIED_VERSION=migrate-fails
	run_deploy

	assert_status 1
	assert_not_called "compose up"
	assert_output_has "syntax error"
	assert_output_lacks "hunter2"
	teardown
}

test_rollback_returns_to_the_bookmarked_tag() {
	setup "rollback swaps back to the bookmarked tag"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:newsha"
	bookmark_fixture oldsha no
	run_deploy rollback

	assert_status 0
	assert_output_has "oldsha"
	assert_called "docker pull ghcr.io/abir66/mdfly:oldsha"
	assert_call_order "compose --profile green up" "compose stop app_blue"
	assert_not_called "pull --ff-only"
	grep -q "MDFLY_IMAGE=ghcr.io/abir66/mdfly:oldsha" "$SANDBOX/deploy/.env" ||
		fail "expected the compose image file to name the bookmarked tag"
	grep -q "PREVIOUS_TAG=newsha" "$SANDBOX/deploy/.deploy-state" ||
		fail "expected the rolled-back-from tag to be bookmarked in turn"
	teardown
}

test_rollback_across_a_migration_refuses_without_force() {
	setup "rollback across a migration boundary warns and stops"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:newsha"
	bookmark_fixture oldsha yes
	run_deploy rollback

	assert_status 1
	assert_output_has "WARNING"
	assert_output_has "--force"
	assert_not_called "compose up"

	run_deploy rollback --force
	assert_status 0
	assert_output_has "WARNING"
	assert_called "compose --profile green up"
	teardown
}

test_rollback_without_a_bookmark_stops() {
	setup "rollback with nothing bookmarked stops"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:newsha"
	run_deploy rollback

	assert_status 1
	assert_not_called "compose up"
	teardown
}

test_prune_keeps_only_the_serving_and_previous_tags() {
	setup "prune leaves the serving and rollback images"
	STUB_RUNNING_SLOTS="app_blue"
	STUB_IMAGE_app_blue="ghcr.io/abir66/mdfly:oldsha"
	STUB_LOCAL_TAGS="newsha oldsha ancientsha"
	run_deploy

	assert_status 0
	assert_called "docker rmi ghcr.io/abir66/mdfly:ancientsha"
	assert_not_called "docker rmi ghcr.io/abir66/mdfly:oldsha"
	assert_not_called "docker rmi ghcr.io/abir66/mdfly:newsha"
	teardown
}

main() {
	for t in $(declare -F | awk '{print $3}' | grep '^test_'); do
		"$t"
	done
	if [ "$FAILURES" -gt 0 ]; then
		printf '\n%d assertion(s) failed\n' "$FAILURES" >&2
		exit 1
	fi
	echo "deploy.sh: all tests passed"
}

main "$@"

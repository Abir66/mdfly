#!/usr/bin/env bash
# The deploy (ADR-0015). Nothing is compiled here: CI publishes an ARM image per
# commit and this pulls it. What is not in the image — compose.yaml, the Caddyfile,
# migrations/ — comes from the repo, so the deploy starts by pulling that.
#
#   ./deploy.sh [--dry-run] [<tag>]        deploy <tag>, default the pulled HEAD
#   ./deploy.sh rollback [--force] [--dry-run]   return to the bookmarked tag
#
# Run it from a checkout on the box. --dry-run reads the applied schema version and
# prints what would happen; it starts nothing and pulls nothing.
#
# .env is passed through to Compose and never parsed here, and no path prints a
# command's raw output without running it through `redact` first.
set -Eeuo pipefail

IMAGE_REPO=ghcr.io/abir66/mdfly
# Matches `name:` in compose.yaml; used to find containers by label, which works
# regardless of which profiles are enabled.
COMPOSE_PROJECT=mdfly
SLOT_BLUE=app_blue
SLOT_GREEN=app_green
HEALTH_PORT=8080
HEALTH_PATH=/healthz
HEALTH_INTERVAL_SECONDS=2
# Long enough for a boot and its startup DB ping on a free-tier box.
HEALTH_TIMEOUT_SECONDS=${MDFLY_HEALTH_TIMEOUT:-60}

DEPLOY_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$DEPLOY_DIR/.." && pwd)
# Compose reads this for ${MDFLY_IMAGE} interpolation. Not the application's .env,
# which stays at the repo root and is never parsed by this script.
IMAGE_ENV_FILE="$DEPLOY_DIR/.env"
STATE_FILE="$DEPLOY_DIR/.deploy-state"

usage() {
	cat <<'EOF'
usage: deploy.sh [--dry-run] [<tag>]
       deploy.sh rollback [--force] [--dry-run]
EOF
}

DRY_RUN=0
FORCE=0
PREVIOUS_TAG=""
CROSSED_MIGRATION=no

main() {
	local command=deploy tag=""
	while [ $# -gt 0 ]; do
		case "$1" in
		--dry-run) DRY_RUN=1 ;;
		--force) FORCE=1 ;;
		rollback) command=rollback ;;
		-h | --help)
			usage
			exit 0
			;;
		-*)
			usage >&2
			exit 2
			;;
		*) tag=$1 ;;
		esac
		shift
	done
	"cmd_$command" "$tag"
}

cmd_deploy() {
	local tag=$1
	[ "$DRY_RUN" = 1 ] || git -C "$REPO_ROOT" pull --ff-only
	[ -n "$tag" ] || tag=$(git -C "$REPO_ROOT" rev-parse HEAD)

	detect_state
	print_plan "$DEPLOY_PATH" "$tag"
	[ "$DRY_RUN" = 1 ] && return 0
	execute "$DEPLOY_PATH" "$tag"
}

# Code only, always: the bookmark holds a tag, and a tag cannot un-apply a
# migration. Hence the refusal below rather than a schema-aware rollback.
cmd_rollback() {
	[ -z "$1" ] || die "rollback takes no tag — it returns to the bookmarked one"
	load_bookmark
	warn_migration_boundary
	detect_state

	local path=swap
	[ -n "$LIVE_SLOT" ] || path=cold-start
	print_plan "$path" "$PREVIOUS_TAG"
	[ "$DRY_RUN" = 1 ] && return 0
	execute "$path" "$PREVIOUS_TAG"
}

execute() {
	local path=$1 tag=$2
	docker pull "$IMAGE_REPO:$tag"
	bookmark "$CURRENT_TAG" "$path"
	set_compose_image "$tag"
	case "$path" in
	swap) swap_slots "$LIVE_SLOT" "$IDLE_SLOT" "$CURRENT_TAG" ;;
	cold-start) cold_start "$IDLE_SLOT" ;;
	migrate) migrate_in_place "${LIVE_SLOT:-$IDLE_SLOT}" ;;
	esac
	refresh_jobs
	prune_images "$tag" "$CURRENT_TAG"
}

# --- the two paths ----------------------------------------------------------

# Zero downtime: both versions are up for a few seconds, and Caddy's `lb_policy
# first` means traffic flips the instant the live slot stops.
swap_slots() {
	local live=$1 idle=$2 previous=$3
	start_slot "$idle"
	if ! await_health "$idle"; then
		echo "deploy: $idle never answered $HEALTH_PATH — aborting, $live keeps serving" >&2
		stop_slot "$idle"
		set_compose_image "$previous"
		exit 1
	fi
	stop_slot "$live"
}

# No overlap, because a migration would otherwise have to be readable by both
# versions at once (ADR-0015). Costs a few seconds of downtime.
migrate_in_place() {
	local slot=$1 out
	if ! out=$(docker compose run --rm -T migrate \
		'migrate -path=/migrations -database="$DATABASE_URL" up' 2>&1); then
		printf '%s\n' "$out" | redact >&2
		die "the migration failed; no new code was started"
	fi
	start_slot "$slot" --force-recreate
	await_health "$slot" ||
		die "$slot did not come back after the migration — the site is down, fix forward"
}

# Nothing is serving, so there is no slot to preserve: bring the whole default
# profile up, which is Caddy, Redis, jobs and blue.
cold_start() {
	local slot=$1
	docker compose up --detach
	await_health "$slot" || die "$slot never answered $HEALTH_PATH"
}

# --- container operations ---------------------------------------------------

start_slot() {
	local slot=$1
	shift
	compose_for_slot "$slot" up --detach "$@" "$slot"
}

stop_slot() { compose_for_slot "$1" stop "$1"; }

await_health() {
	local slot=$1 waited=0
	while [ "$waited" -lt "$HEALTH_TIMEOUT_SECONDS" ]; do
		if compose_for_slot "$slot" exec -T "$slot" \
			wget -qO- "http://127.0.0.1:$HEALTH_PORT$HEALTH_PATH" >/dev/null 2>&1; then
			return 0
		fi
		sleep "$HEALTH_INTERVAL_SECONDS"
		waited=$((waited + HEALTH_INTERVAL_SECONDS))
	done
	return 1
}

# Green is behind a compose profile so a bare `up` never starts both slots.
compose_for_slot() {
	local slot=$1
	shift
	if [ "$slot" = "$SLOT_GREEN" ]; then
		docker compose --profile green "$@"
	else
		docker compose "$@"
	fi
}

# The jobs process runs the same image and has no slots — it is simply replaced.
# Nothing waits on it, and its 120s grace period lets a pass in flight finish.
refresh_jobs() { docker compose up --detach jobs; }

# Steady state is two images: what is serving and what rollback would return to.
prune_images() {
	local keep_new=$1 keep_old=$2 tag
	docker image prune --force >/dev/null
	for tag in $(docker images "$IMAGE_REPO" --format '{{.Tag}}'); do
		case "$tag" in "$keep_new" | "$keep_old" | "<none>") continue ;; esac
		if docker rmi "$IMAGE_REPO:$tag" >/dev/null 2>&1; then
			echo "pruned $IMAGE_REPO:$tag"
		fi
	done
}

# --- state ------------------------------------------------------------------

# Records what to go back to, before anything is swapped. A deploy that crossed a
# migration is marked, because rollback only moves code.
bookmark() {
	local previous=$1 path=$2 crossed=no
	[ "$path" = migrate ] && crossed=yes
	cat >"$STATE_FILE" <<EOF
PREVIOUS_TAG=$previous
CROSSED_MIGRATION=$crossed
EOF
}

load_bookmark() {
	[ -f "$STATE_FILE" ] || die "nothing is bookmarked; there is no deploy to undo"
	# shellcheck source=/dev/null
	. "$STATE_FILE"
	[ "$PREVIOUS_TAG" = none ] &&
		die "the bookmarked deploy was a cold start; there is no earlier tag"
	return 0
}

# Rollback moves code and nothing else, so returning across a migration puts the
# old binary in front of a schema it has never seen.
warn_migration_boundary() {
	[ "$CROSSED_MIGRATION" = yes ] || return 0
	cat >&2 <<EOF
WARNING: the deploy being reverted applied a migration. $PREVIOUS_TAG predates the
current schema and may fail against it. Rolling back cannot un-apply a migration —
fixing forward is usually the safer move.
EOF
	[ "$FORCE" = 1 ] || die "refusing to roll back across a migration without --force"
	return 0
}

set_compose_image() {
	cat >"$IMAGE_ENV_FILE" <<EOF
# Generated by deploy.sh. No secrets here — the application's environment is the
# repo-root .env, which compose loads per service via env_file.
MDFLY_IMAGE=$IMAGE_REPO:$1
EOF
}

# --- detection --------------------------------------------------------------

# Sets LIVE_SLOT (empty on a cold start), IDLE_SLOT, CURRENT_TAG and DEPLOY_PATH.
# Globals rather than echoed values so that `die` here exits the deploy — inside a
# command substitution it would only end the subshell.
detect_state() {
	local blue green
	blue=$(slot_container "$SLOT_BLUE")
	green=$(slot_container "$SLOT_GREEN")
	# Both up is not a state a deploy can reason about: Caddy is serving whichever
	# it prefers, so there is no "live" slot to swap away from.
	[ -n "$blue" ] && [ -n "$green" ] &&
		die "both $SLOT_BLUE and $SLOT_GREEN are running; stop one and retry"

	if [ -n "$blue" ]; then
		LIVE_SLOT=$SLOT_BLUE
		IDLE_SLOT=$SLOT_GREEN
	else
		LIVE_SLOT=${green:+$SLOT_GREEN}
		IDLE_SLOT=$SLOT_BLUE
	fi
	CURRENT_TAG=$(slot_tag "$LIVE_SLOT")
	detect_deploy_path
}

slot_container() {
	docker ps --quiet \
		--filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
		--filter "label=com.docker.compose.service=$1"
}

slot_tag() {
	local cid image
	[ -n "$1" ] || {
		echo "none"
		return 0
	}
	cid=$(slot_container "$1")
	image=$(docker inspect --format '{{.Config.Image}}' "$cid")
	echo "${image##*:}"
}

# Sets DEPLOY_PATH: "migrate" when migrations/ has outrun the applied version,
# "swap" for a code-only deploy with an overlap, "cold-start" when nothing serves.
# Nothing about this is a flag the operator has to remember.
detect_deploy_path() {
	local latest
	latest=$(latest_migration)
	read_applied_version
	if [ "$latest" -gt "$APPLIED_VERSION" ]; then
		DEPLOY_PATH=migrate
	elif [ -z "$LIVE_SLOT" ]; then
		DEPLOY_PATH=cold-start
	else
		DEPLOY_PATH=swap
	fi
}

# The glob expands in sorted order, so the last match is the highest numbered.
latest_migration() {
	local newest name
	for newest in "$REPO_ROOT"/migrations/*.up.sql; do :; done
	name=$(basename "$newest")
	echo "$((10#${name%%_*}))"
}

# Read through the compose `migrate` service so DATABASE_URL is expanded inside the
# container from env_file — this script never sees it. The version lands on stderr,
# and a connection failure can carry the DSN, so the output is redacted before it
# reaches a terminal.
read_applied_version() {
	local raw version
	raw=$(docker compose run --rm -T migrate \
		'migrate -path=/migrations -database="$DATABASE_URL" version' 2>&1) || true
	version=$(printf '%s' "$raw" | tail -1 | tr -d '[:space:]')
	case "$version" in
	*"nomigration"*) APPLIED_VERSION=0 ;;
	*dirty*) die "the schema is marked dirty; resolve it before deploying" ;;
	*[!0-9]* | "")
		printf '%s\n' "$raw" | redact >&2
		die "could not read the applied schema version"
		;;
	*) APPLIED_VERSION=$version ;;
	esac
}

# --- reporting --------------------------------------------------------------

print_plan() {
	local path=$1 target=$2
	echo "path:    $(path_description "$path")"
	echo "current: $CURRENT_TAG"
	echo "target:  $target"
	case "$path" in
	swap | cold-start) echo "serving: $IDLE_SLOT (was ${LIVE_SLOT:-none})" ;;
	migrate) echo "serving: ${LIVE_SLOT:-$IDLE_SLOT} (recreated in place)" ;;
	esac
}

path_description() {
	case "$1" in
	swap) echo "code-only — start the idle slot, then stop the live one" ;;
	migrate) echo "schema change — migrate, then recreate the live slot" ;;
	cold-start) echo "cold start — no slot is serving" ;;
	esac
}

die() {
	echo "deploy: $*" >&2
	exit 1
}

# A failing command's own output is the only place a secret can reach the terminal,
# and in practice it arrives as a connection string.
redact() { sed -E 's#[a-zA-Z][a-zA-Z0-9+.-]*://[^[:space:]]*#<redacted>#g'; }

main "$@"

#!/usr/bin/env bash
set -Eeuo pipefail

SOURCE_DIR="/opt/vpsdeck/src"
BRANCH="${VPSDECK_BRANCH:-main}"
BACKUP_DIR="/var/backups/vpsdeck"
TEMP_BINARY="/tmp/vpsdeck-update"
GO_BIN="/usr/local/go/bin/go"
OLD_REVISION=""
BACKUP_BINARY=""

log() {
  printf '[VPSDeck] %s\n' "$*"
}

fail() {
  printf '[VPSDeck] ERROR: %s\n' "$*" >&2
  exit 1
}

rollback() {
  local status=$?
  if [[ "$status" -eq 0 ]]; then
    return
  fi
  log "Update failed; restoring the previous release"
  if [[ -n "$BACKUP_BINARY" && -f "$BACKUP_BINARY" ]]; then
    install -m 0755 "$BACKUP_BINARY" /usr/local/bin/vpsdeck
  fi
  if [[ -n "$OLD_REVISION" ]]; then
    git -C "$SOURCE_DIR" reset --hard "$OLD_REVISION" >/dev/null 2>&1 || true
  fi
  systemctl restart vpsdeck >/dev/null 2>&1 || true
  exit "$status"
}
trap rollback EXIT

[[ "${EUID}" -eq 0 ]] || fail "run this updater as root"
[[ -d "$SOURCE_DIR/.git" ]] || fail "VPSDeck source checkout not found at $SOURCE_DIR"
[[ -x "$GO_BIN" ]] || fail "Go is not installed at $GO_BIN"

install -d -m 0750 -o vpsdeck -g vpsdeck "$BACKUP_DIR"
OLD_REVISION="$(git -C "$SOURCE_DIR" rev-parse HEAD)"
BACKUP_BINARY="$BACKUP_DIR/vpsdeck-$(date -u +%Y%m%dT%H%M%SZ)"
cp -a /usr/local/bin/vpsdeck "$BACKUP_BINARY"

log "Fetching $BRANCH"
git -C "$SOURCE_DIR" fetch --prune origin "$BRANCH"
git -C "$SOURCE_DIR" checkout -B "$BRANCH" "origin/$BRANCH"

log "Running tests and building"
(
  cd "$SOURCE_DIR"
  "$GO_BIN" test ./...
  CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$TEMP_BINARY" ./cmd/server
)

install -m 0755 "$TEMP_BINARY" /usr/local/bin/vpsdeck
rm -f "$TEMP_BINARY"
systemctl restart vpsdeck

for attempt in {1..30}; do
  if curl -fsS http://127.0.0.1:8080/healthz >/dev/null; then
    trap - EXIT
    log "Updated from $OLD_REVISION to $(git -C "$SOURCE_DIR" rev-parse HEAD)"
    exit 0
  fi
  sleep 1
done

fail "health check failed after update"

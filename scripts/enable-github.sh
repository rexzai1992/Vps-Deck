#!/usr/bin/env bash
set -Eeuo pipefail

# Turns on VPSDeck's GitHub OAuth integration. Run as root after creating a
# GitHub OAuth App (Settings -> Developer settings -> OAuth Apps) whose
# Authorization callback URL is https://<your-domain>/integrations/github/callback

ENV_FILE="/etc/vpsdeck/github.env"
SERVICE_FILE="/etc/systemd/system/vpsdeck.service"
DOMAIN="${VPSDECK_DOMAIN:-vps.izzul.xyz}"
CALLBACK="${VPSDECK_GITHUB_CALLBACK_URL:-https://${DOMAIN}/integrations/github/callback}"

log() { printf '[VPSDeck] %s\n' "$*"; }
fail() { printf '[VPSDeck] ERROR: %s\n' "$*" >&2; exit 1; }

[[ "${EUID}" -eq 0 ]] || fail "run this as root"

CLIENT_ID="${VPSDECK_GITHUB_CLIENT_ID:-}"
CLIENT_SECRET="${VPSDECK_GITHUB_CLIENT_SECRET:-}"
if [[ -z "$CLIENT_ID" ]]; then
  read -r -p "GitHub OAuth App Client ID: " CLIENT_ID
fi
if [[ -z "$CLIENT_SECRET" ]]; then
  read -r -s -p "GitHub OAuth App Client Secret: " CLIENT_SECRET
  printf '\n'
fi
[[ -n "$CLIENT_ID" && -n "$CLIENT_SECRET" ]] || fail "client id and secret are required"

# Preserve an existing token key so already-connected accounts keep working.
TOKEN_KEY=""
if [[ -f "$ENV_FILE" ]]; then
  TOKEN_KEY="$(grep -E '^VPSDECK_GITHUB_TOKEN_KEY=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)"
fi
if [[ -z "$TOKEN_KEY" ]]; then
  TOKEN_KEY="$(openssl rand -base64 32)"
  log "Generated a new token encryption key"
fi

install -d -m 0755 /etc/vpsdeck
umask 027
cat >"$ENV_FILE" <<EOF
VPSDECK_GITHUB_ENABLED=true
VPSDECK_GITHUB_CLIENT_ID=${CLIENT_ID}
VPSDECK_GITHUB_CLIENT_SECRET=${CLIENT_SECRET}
VPSDECK_GITHUB_CALLBACK_URL=${CALLBACK}
VPSDECK_GITHUB_TOKEN_KEY=${TOKEN_KEY}
EOF
chown root:vpsdeck "$ENV_FILE"
chmod 0640 "$ENV_FILE"
log "Wrote $ENV_FILE (callback: $CALLBACK)"

if [[ -f "$SERVICE_FILE" ]] && ! grep -q 'github.env' "$SERVICE_FILE"; then
  log "WARNING: $SERVICE_FILE has no EnvironmentFile for github.env."
  log "         Re-run scripts/install.sh to update the unit, then retry."
fi

systemctl daemon-reload
systemctl restart vpsdeck
log "Restarted vpsdeck. Open Add Project -> Deploy from GitHub -> Connect GitHub."

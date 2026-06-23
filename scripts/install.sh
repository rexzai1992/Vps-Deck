#!/usr/bin/env bash
set -Eeuo pipefail

REPOSITORY="${VPSDECK_REPOSITORY:-https://github.com/rexzai1992/Vps-Deck.git}"
BRANCH="${VPSDECK_BRANCH:-main}"
DOMAIN="${VPSDECK_DOMAIN:-vps.izzul.xyz}"
SOURCE_DIR="/opt/vpsdeck/src"
CONFIG_DIR="/etc/vpsdeck"
CONFIG_PATH="$CONFIG_DIR/config.yaml"
SERVICE_PATH="/etc/systemd/system/vpsdeck.service"
NGINX_AVAILABLE="/etc/nginx/sites-available/vpsdeck"
NGINX_ENABLED="/etc/nginx/sites-enabled/vpsdeck"

log() {
  printf '[VPSDeck] %s\n' "$*"
}

fail() {
  printf '[VPSDeck] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  [[ "${EUID}" -eq 0 ]] || fail "run this installer as root"
}

validate_domain() {
  [[ "$DOMAIN" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]] ||
    fail "invalid domain: $DOMAIN"
}

install_packages() {
  log "Installing required Ubuntu packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ca-certificates curl git nginx certbot python3-certbot-nginx
}

install_go() {
  local architecture go_arch version archive temporary checksum
  architecture="$(dpkg --print-architecture)"
  case "$architecture" in
    amd64) go_arch="amd64" ;;
    arm64) go_arch="arm64" ;;
    *) fail "unsupported architecture: $architecture" ;;
  esac

  version="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n 1)"
  [[ "$version" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || fail "could not determine the current Go release"
  archive="${version}.linux-${go_arch}.tar.gz"
  temporary="$(mktemp -d)"
  trap 'rm -rf "$temporary"' RETURN

  log "Installing $version"
  curl -fsSL "https://go.dev/dl/${archive}" -o "$temporary/$archive"
  checksum="$(curl -fsSL "https://dl.google.com/go/${archive}.sha256")"
  printf '%s  %s\n' "$checksum" "$temporary/$archive" | sha256sum --check --status ||
    fail "Go archive checksum verification failed"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$temporary/$archive"
  rm -rf "$temporary"
  trap - RETURN
}

create_account_and_directories() {
  if ! id vpsdeck >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/vpsdeck --create-home --shell /usr/sbin/nologin vpsdeck
  fi
  if getent group docker >/dev/null 2>&1; then
    usermod -aG docker vpsdeck
  fi

  install -d -m 0750 -o vpsdeck -g vpsdeck \
    /var/lib/vpsdeck /var/log/vpsdeck /var/backups/vpsdeck /opt/apps
  install -d -m 0755 /opt/vpsdeck "$CONFIG_DIR"
}

checkout_source() {
  log "Checking out VPSDeck $BRANCH"
  if [[ -d "$SOURCE_DIR/.git" ]]; then
    git -C "$SOURCE_DIR" remote set-url origin "$REPOSITORY"
    git -C "$SOURCE_DIR" fetch --prune origin "$BRANCH"
    git -C "$SOURCE_DIR" checkout -B "$BRANCH" "origin/$BRANCH"
  else
    rm -rf "$SOURCE_DIR"
    git clone --branch "$BRANCH" --single-branch "$REPOSITORY" "$SOURCE_DIR"
  fi
}

build_binary() {
  log "Building VPSDeck"
  (
    cd "$SOURCE_DIR"
    CGO_ENABLED=0 /usr/local/go/bin/go build -trimpath -ldflags='-s -w' -o /tmp/vpsdeck ./cmd/server
  )
  install -m 0755 /tmp/vpsdeck /usr/local/bin/vpsdeck
  rm -f /tmp/vpsdeck
}

write_configuration() {
  if [[ -f "$CONFIG_PATH" ]]; then
    log "Keeping existing configuration at $CONFIG_PATH"
    return
  fi

  log "Writing production configuration"
  cat >"$CONFIG_PATH" <<EOF
app:
  name: VPSDeck
  host: 127.0.0.1
  port: 8080
  base_url: https://${DOMAIN}
  environment: production

security:
  cookie_secure: true
  session_lifetime_hours: 12
  login_rate_limit_per_minute: 5
  advanced_mode:
    enabled: true
    root: /
    timeout_minutes: 15
    second_password: ""

paths:
  database: /var/lib/vpsdeck/vpsdeck.db
  data_dir: /var/lib/vpsdeck
  log_dir: /var/log/vpsdeck
  backup_dir: /var/backups/vpsdeck
  apps_dir: /opt/apps
  simple_mode_roots:
    - /var/www
    - /opt/apps
    - /srv
    - /home/apps

deployments:
  enabled: true
  git_command: git
  timeout_seconds: 300

updates:
  enabled: true
  source_dir: /opt/vpsdeck/src
  branch: ${BRANCH}
  git_command: git
  check_interval_minutes: 30
  request_path: /var/lib/vpsdeck/update.request
  status_path: /var/lib/vpsdeck/update.status

monitoring:
  refresh_seconds: 5
  ports:
    enabled: true
  ollama:
    enabled: true
    base_url: http://127.0.0.1:11434
    timeout_seconds: 2

docker:
  enabled: true
  discovery_enabled: true
  command: docker
  timeout_seconds: 10
EOF
  chmod 0640 "$CONFIG_PATH"
  chown root:vpsdeck "$CONFIG_PATH"
}

write_github_env_template() {
  local env_file="$CONFIG_DIR/github.env"
  if [[ -f "$env_file" ]]; then
    log "Keeping existing GitHub integration settings at $env_file"
    return
  fi
  log "Writing GitHub integration template (disabled until you add OAuth credentials)"
  cat >"$env_file" <<EOF
# Fill in the OAuth App credentials, set ENABLED=true, then restart vpsdeck.
# Or simply run: sudo $SOURCE_DIR/scripts/enable-github.sh
VPSDECK_GITHUB_ENABLED=false
VPSDECK_GITHUB_CLIENT_ID=
VPSDECK_GITHUB_CLIENT_SECRET=
VPSDECK_GITHUB_CALLBACK_URL=https://${DOMAIN}/integrations/github/callback
VPSDECK_GITHUB_TOKEN_KEY=$(openssl rand -base64 32)
EOF
  chmod 0640 "$env_file"
  chown root:vpsdeck "$env_file"
}

bootstrap_administrator() {
  if /usr/local/bin/vpsdeck -config "$CONFIG_PATH" -bootstrap-only >/dev/null 2>&1; then
    log "An administrator already exists"
    return
  fi

  local username password confirmation
  username="${VPSDECK_ADMIN_USERNAME:-}"
  password="${VPSDECK_ADMIN_PASSWORD:-}"
  if [[ -z "$username" ]]; then
    read -r -p "VPSDeck admin username [admin]: " username
    username="${username:-admin}"
  fi
  if [[ -z "$password" ]]; then
    while true; do
      read -r -s -p "VPSDeck admin password (12+ chars, upper/lower/number): " password
      printf '\n'
      read -r -s -p "Confirm VPSDeck admin password: " confirmation
      printf '\n'
      [[ "$password" == "$confirmation" ]] && break
      printf 'Passwords do not match. Try again.\n' >&2
    done
  fi

  log "Creating the initial administrator"
  VPSDECK_ADMIN_USERNAME="$username" VPSDECK_ADMIN_PASSWORD="$password" \
    /usr/local/bin/vpsdeck -config "$CONFIG_PATH" -bootstrap-only
  unset password confirmation VPSDECK_ADMIN_PASSWORD
  chown -R vpsdeck:vpsdeck /var/lib/vpsdeck /var/log/vpsdeck /var/backups/vpsdeck /opt/apps
}

install_service() {
  install -m 0644 "$SOURCE_DIR/scripts/vpsdeck.service" "$SERVICE_PATH"
  # Privileged, path-activated self-updater. The panel runs unprivileged and only
  # drops a request flag; this root service rebuilds and restarts VPSDeck.
  install -m 0644 "$SOURCE_DIR/scripts/vpsdeck-update.service" /etc/systemd/system/vpsdeck-update.service
  install -m 0644 "$SOURCE_DIR/scripts/vpsdeck-update.path" /etc/systemd/system/vpsdeck-update.path
  systemctl daemon-reload
  systemctl enable vpsdeck
  systemctl enable --now vpsdeck-update.path
}

install_nginx_site() {
  sed "s/__VPSDECK_DOMAIN__/$DOMAIN/g" "$SOURCE_DIR/scripts/nginx-vpsdeck.conf" >"$NGINX_AVAILABLE"
  ln -sfn "$NGINX_AVAILABLE" "$NGINX_ENABLED"
  nginx -t
  systemctl reload nginx
}

start_and_verify() {
  log "Starting VPSDeck"
  systemctl restart vpsdeck
  local attempt
  for attempt in {1..30}; do
    if curl -fsS http://127.0.0.1:8080/healthz >/dev/null; then
      log "VPSDeck is healthy on 127.0.0.1:8080"
      return
    fi
    sleep 1
  done
  journalctl -u vpsdeck --no-pager -n 50 >&2 || true
  fail "VPSDeck did not become healthy"
}

print_next_steps() {
  cat <<EOF

VPSDeck is installed.

DNS:
  Create an A record: ${DOMAIN} -> $(curl -fsS https://api.ipify.org || hostname -I | awk '{print $1}')

After DNS resolves to this VPS, enable HTTPS:
  certbot --nginx -d ${DOMAIN} --redirect

Then open:
  https://${DOMAIN}

Connect GitHub (optional):
  Create a GitHub OAuth App with callback https://${DOMAIN}/integrations/github/callback
  then run: sudo ${SOURCE_DIR}/scripts/enable-github.sh

Service logs:
  journalctl -u vpsdeck -f
EOF
}

require_root
validate_domain
install_packages
install_go
create_account_and_directories
checkout_source
build_binary
write_configuration
write_github_env_template
bootstrap_administrator
install_service
install_nginx_site
start_and_verify
print_next_steps

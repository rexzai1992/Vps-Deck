#!/usr/bin/env bash
set -Eeuo pipefail

PURGE=false
if [[ "${1:-}" == "--purge" ]]; then
  PURGE=true
elif [[ -n "${1:-}" ]]; then
  printf 'Usage: %s [--purge]\n' "$0" >&2
  exit 2
fi

[[ "${EUID}" -eq 0 ]] || {
  printf 'Run this uninstaller as root.\n' >&2
  exit 1
}

systemctl disable --now vpsdeck 2>/dev/null || true
rm -f /etc/systemd/system/vpsdeck.service
systemctl daemon-reload

rm -f /etc/nginx/sites-enabled/vpsdeck /etc/nginx/sites-available/vpsdeck
if nginx -t >/dev/null 2>&1; then
  systemctl reload nginx
fi

rm -f /usr/local/bin/vpsdeck
rm -rf /opt/vpsdeck

if [[ "$PURGE" == true ]]; then
  read -r -p "Delete VPSDeck config, database, logs, and backups? Type DELETE: " confirmation
  if [[ "$confirmation" == "DELETE" ]]; then
    rm -rf /etc/vpsdeck /var/lib/vpsdeck /var/log/vpsdeck /var/backups/vpsdeck
    userdel vpsdeck 2>/dev/null || true
    printf 'VPSDeck and its data were removed.\n'
  else
    printf 'Data purge cancelled; application files were removed.\n'
  fi
else
  printf 'VPSDeck was removed. Data and configuration were preserved.\n'
fi

#!/usr/bin/env bash
set -Eeuo pipefail

# Local development runner. Loads optional GitHub OAuth secrets from
# configs/github.env (gitignored) so the Connect GitHub button works locally.
cd "$(dirname "$0")/.."

if [[ -f configs/github.env ]]; then
  set -a
  # shellcheck disable=SC1091
  source configs/github.env
  set +a
fi

exec go run ./cmd/server -config configs/config.yaml

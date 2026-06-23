# VPSDeck

VPSDeck is a Go-based web control panel for managing projects and VPS services through a beginner-friendly browser interface.

The current runnable slice includes:

- Secure first-admin bootstrap
- Login/logout with SQLite-backed sessions
- CSRF protection and login rate limiting
- Live CPU, RAM, storage, uptime, OS, kernel, and local-IP dashboard
- Existing-folder project registration with project-type detection
- Strict Simple Mode path-root validation
- Project list/detail/unregister flow
- Windows Explorer–style project file manager: live grid with multi-select, right-click menu, drag-and-drop move/copy, drag-from-desktop multi-upload, inline rename, keyboard shortcuts, and Details/Icons views
- Safe file/folder move, copy (auto-renaming on conflict), rename, download, folder ZIP download, and confirmed recursive delete — all contained to the registered project root
- Browser text editor with automatic pre-save backups
- Key-value `.env` editor with secret masking
- Audit log for login and project actions
- Visual approved-root folder picker when registering projects
- Live TCP/UDP port monitor with project-port matching
- Local or remote Ollama health and model monitor
- Read-only official Ollama installation guidance when unavailable
- Docker Compose auto-discovery from Docker labels, with explicit project import
- Runtime status synchronization and published-port association for imported Compose projects
- One-click GitHub account connect (OAuth App) with a searchable repository and branch picker, including private repositories
- Encrypted-at-rest GitHub token storage and authenticated clones/fetches that never expose the token in a URL or command line
- Self-update: detects new commits on VPSDeck's own branch, shows an Updates page and dashboard banner, and applies the update through a privileged path-activated system service
- Advanced Mode (password re-confirmation, audit, inactivity timeout) unlocking a full-filesystem explorer with the same drag-and-drop file management as the project explorer
- GitHub repository cloning into the managed apps directory
- Audited, fast-forward-only GitHub deployments with dirty-tree protection
- Optional validated Docker Compose rebuilds after Git synchronization
- Deployment history with before/after commit IDs and bounded command output
- Visual `.env` editing followed by an optional one-click deployment

## Run locally

Create an initial administrator on the first run:

```bash
VPSDECK_ADMIN_USERNAME=admin \
VPSDECK_ADMIN_PASSWORD='ChangeThisStrong123' \
go run ./cmd/server
```

Open <http://127.0.0.1:8080>.

After the first administrator is stored, the environment variables are no longer required.

## Test and build

```bash
go test ./...
go build ./cmd/server
```

Configuration lives in `configs/config.yaml`. Production defaults are documented in `configs/config.example.yaml`.

For local development, project folders must be placed under `managed-apps/` unless `simple_mode_roots` is changed.

Monitor another Ollama server by changing `monitoring.ollama.base_url` or setting:

```bash
VPSDECK_OLLAMA_BASE_URL=http://another-server:11434 go run ./cmd/server
```

For remote Ollama, prefer a private network, VPN, or authenticated reverse proxy.

When Docker Compose discovery is enabled, the Projects page lists running and stopped Compose stacks found on the server. Importing a detected stack registers its trusted Docker-reported working directory, published host port, and runtime status in VPSDeck.

## Deploy from GitHub

Open **Add Project → Deploy from GitHub**, then provide a public HTTPS repository URL, branch, project name, and deployment mode.

VPSDeck clones the repository into the configured `apps_dir`, opens the visual `.env` editor, and enables **Deploy latest** on the project page. Deployments:

- refuse tracked local changes instead of overwriting them;
- fetch only the configured branch;
- use a fast-forward-only merge;
- record commit IDs, status, output, errors, and audit events;
- optionally validate and run `docker compose up -d --build`.

Repository URLs containing credentials are rejected.

## Connect a GitHub account (OAuth)

To browse and deploy your repositories — including private ones — without pasting URLs, enable the GitHub OAuth App integration.

1. Create a GitHub OAuth App (Settings → Developer settings → OAuth Apps) with the Authorization callback URL `https://your-domain/integrations/github/callback`.
2. Provide the credentials and a 32-byte AES key (base64) to VPSDeck via environment variables (preferred over committing them to YAML):

```bash
export VPSDECK_GITHUB_ENABLED=true
export VPSDECK_GITHUB_CLIENT_ID=...           # from the OAuth App
export VPSDECK_GITHUB_CLIENT_SECRET=...        # from the OAuth App
export VPSDECK_GITHUB_CALLBACK_URL=https://your-domain/integrations/github/callback
export VPSDECK_GITHUB_TOKEN_KEY=$(openssl rand -base64 32)
```

Then open **Add Project → Deploy from GitHub → Connect GitHub**. The access token is encrypted at rest with the key and is never written into a repository URL or command line. Keep `VPSDECK_GITHUB_TOKEN_KEY` stable — rotating it invalidates stored tokens (reconnect the account).

## Advanced Mode (full-filesystem explorer)

Simple Mode keeps file management scoped to registered projects. To browse and edit the whole server, open **Advanced Mode** (sidebar → Advanced), re-enter your password (and an optional configured second password), and a time-limited session unlocks **System Files** — the same Explorer-style manager rooted at `/`. It turns off automatically after the configured timeout, and every entry/exit is audited.

Configure it under `security.advanced_mode` (enabled, `root`, `timeout_minutes`, optional `second_password`). Operations are still bound by the `vpsdeck` OS user's own permissions.

## Update the panel

VPSDeck watches its own GitHub branch and shows an **Updates** page (and a dashboard banner) when a newer commit is available. Press **Update now** to rebuild and restart. Because the panel runs unprivileged, applying an update is handled by a root-owned, path-activated system service installed by `scripts/install.sh` — existing installs must re-run the installer once. See [`docs/INSTALL.md`](docs/INSTALL.md#update-and-rollback).

Read `VPSDECK_PROJECT_CONTEXT.md` before continuing development.

## Install on an Ubuntu VPS

VPSDeck includes a production installer that creates a dedicated system user, builds the Go binary, configures systemd, and adds an isolated Nginx virtual host:

```bash
git clone https://github.com/rexzai1992/Vps-Deck.git /tmp/vpsdeck-installer
sudo bash /tmp/vpsdeck-installer/scripts/install.sh
```

See [`docs/INSTALL.md`](docs/INSTALL.md) for DNS, HTTPS, updates, rollback, permissions, and uninstall instructions.

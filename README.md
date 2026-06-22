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
- Project-scoped file browser
- Safe file upload, download, folder creation, and empty-folder/file deletion
- Browser text editor with automatic pre-save backups
- Key-value `.env` editor with secret masking
- Audit log for login and project actions
- Visual approved-root folder picker when registering projects
- Live TCP/UDP port monitor with project-port matching
- Local or remote Ollama health and model monitor
- Read-only official Ollama installation guidance when unavailable
- Docker Compose auto-discovery from Docker labels, with explicit project import
- Runtime status synchronization and published-port association for imported Compose projects

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

Read `VPSDECK_PROJECT_CONTEXT.md` before continuing development.

## Install on an Ubuntu VPS

VPSDeck includes a production installer that creates a dedicated system user, builds the Go binary, configures systemd, and adds an isolated Nginx virtual host:

```bash
git clone https://github.com/rexzai1992/Vps-Deck.git /tmp/vpsdeck-installer
sudo bash /tmp/vpsdeck-installer/scripts/install.sh
```

See [`docs/INSTALL.md`](docs/INSTALL.md) for DNS, HTTPS, updates, rollback, permissions, and uninstall instructions.

# VPSDeck

**A browser-based control panel for your VPS — no terminal required.**

VPSDeck replaces the terminal for everyday server tasks: deploy from GitHub, manage files, monitor resources, and keep Ollama models in check — all from a clean web UI that runs on your own machine.

> **Live demo →** [vps.izzul.xyz](https://vps.izzul.xyz) &nbsp;·&nbsp; sign in with `demo` / `demo`

---

## What it does

| Area | Features |
|---|---|
| **Dashboard** | Live CPU, RAM, disk, uptime, kernel, OS, and local IP |
| **Projects** | Register existing app folders, auto-detects project type |
| **Deploy** | Clone from GitHub, fast-forward-only git deploys, Docker Compose rebuilds |
| **File Manager** | Explorer-style drag-and-drop, multi-select, rename, ZIP download, text editor |
| **Environment** | Key-value `.env` editor with secret masking |
| **Ports** | Live TCP/UDP port monitor with project-port matching |
| **Ollama** | Health, version, installed and loaded model monitor + playground |
| **Docker** | Auto-discovers Compose stacks, imports them as projects |
| **Updates** | Watches its own branch, one-click update + restart |
| **Audit log** | Every login, deploy, and file action is logged |
| **Advanced Mode** | Full-filesystem explorer unlocked by password re-confirm + timeout |
| **GitHub OAuth** | Browse private repos, encrypted-at-rest token, never in a URL |

---

## Screenshots

> Visit **[vps.izzul.xyz](https://vps.izzul.xyz)** to explore the full UI live. Sign in with `demo` / `demo`.

---

## Quick start (local)

```bash
git clone https://github.com/rexzai1992/Vps-Deck.git
cd Vps-Deck

VPSDECK_ADMIN_USERNAME=admin \
VPSDECK_ADMIN_PASSWORD='ChangeThisStrong123' \
go run ./cmd/server
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). After the first admin is created the env vars are no longer needed.

```bash
go test ./...          # run tests
go build ./cmd/server  # build binary
```

Config lives in `configs/config.yaml`. A fully-annotated example is at `configs/config.example.yaml`.

---

## Install on Ubuntu VPS (production)

The installer creates a system user, builds the binary, configures systemd, and sets up an Nginx virtual host:

```bash
git clone https://github.com/rexzai1992/Vps-Deck.git /tmp/vpsdeck-src
sudo bash /tmp/vpsdeck-src/scripts/install.sh
```

See [`docs/INSTALL.md`](docs/INSTALL.md) for DNS setup, HTTPS, rollback, and uninstall.

---

## Demo mode

Run a read-only public demo with realistic fake data — no real system access, all write actions disabled:

```bash
VPSDECK_DEMO_MODE=true go run ./cmd/server
```

Auto-creates a `demo` / `demo` login and seeds the UI with fake projects, deployments, ports, and Ollama models. Safe to expose publicly.

---

## Deploy from GitHub

1. Open **Add Project → Deploy from GitHub**
2. Enter a public HTTPS repo URL, branch, and project name
3. VPSDeck clones it into `apps_dir`, opens the `.env` editor, and enables **Deploy latest** on the project page

Deployments are fast-forward-only, refuse dirty trees, record commit IDs and output, and optionally run `docker compose up -d --build`.

---

## GitHub OAuth (private repos)

```bash
export VPSDECK_GITHUB_ENABLED=true
export VPSDECK_GITHUB_CLIENT_ID=your_client_id
export VPSDECK_GITHUB_CLIENT_SECRET=your_client_secret
export VPSDECK_GITHUB_CALLBACK_URL=https://your-domain/integrations/github/callback
export VPSDECK_GITHUB_TOKEN_KEY=$(openssl rand -base64 32)
```

Create the OAuth App at **GitHub → Settings → Developer settings → OAuth Apps**. Tokens are AES-encrypted at rest and never appear in URLs or shell commands.

---

## Ollama monitor

Shows version, installed models, loaded models, and VRAM usage for a local or remote Ollama instance:

```bash
VPSDECK_OLLAMA_BASE_URL=http://another-server:11434 go run ./cmd/server
```

For remote Ollama, use a private network, VPN, or authenticated reverse proxy.

---

## Advanced Mode

Simple Mode keeps file access scoped to registered projects. **Advanced Mode** unlocks a full-filesystem Explorer rooted at `/`, secured by password re-confirmation and an inactivity timeout. Every entry and exit is audited. Configure under `security.advanced_mode` in `config.yaml`.

---

## Self-update

VPSDeck watches its own GitHub branch and shows a banner when a new commit is available. Click **Update now** — the panel rebuilds and restarts via a root-owned systemd path unit installed by `scripts/install.sh`.

---

## Security

- Bcrypt password hashing
- SHA-256 session tokens in HttpOnly cookies
- CSRF double-submit cookie
- Login rate limiting
- Strict Simple Mode path-root validation (no path traversal)
- GitHub tokens AES-encrypted at rest, never in URLs
- Repository URLs with embedded credentials are rejected

---

## Tech stack

- **Go 1.25** — single binary, no runtime dependencies
- **Gin** — HTTP router and middleware
- **html/template** — server-rendered, no JavaScript framework
- **modernc.org/sqlite** — pure-Go SQLite, no CGo
- **gopsutil** — cross-platform system metrics

---

## License

MIT

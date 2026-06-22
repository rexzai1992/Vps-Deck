# VPSDeck Project Context

> This is the persistent source of truth for continuing VPSDeck development.
> At the start of every new Codex session, say:
>
> **Read `VPSDECK_PROJECT_CONTEXT.md`, inspect the repository, and continue from the first unchecked task. Update the context file before finishing.**

## 1. Project Identity

- Product: **VPSDeck**
- Purpose: A secure, beginner-friendly web control panel for managing a VPS from a browser.
- Backend language: **Go**
- Current repository: `/Users/cravemac2/Vps Manager`
- Initial state on 2026-06-22: Empty repository; implementation has not started.
- Current phase: **Phase 1 implementation**

VPSDeck should combine the useful ideas of cPanel, Portainer, Coolify, Vercel's dashboard, a simple hosting panel, and a Google Drive-style file manager without exposing unnecessary Linux complexity to normal users.

## 2. Product Goal

A non-technical administrator should be able to:

1. Log in securely from a browser.
2. See CPU, memory, storage, uptime, network, and server health.
3. Add a project from Git, ZIP, uploaded files, Docker Compose, an existing folder, or a blank project.
4. Browse, upload, download, edit, move, copy, rename, and delete project files.
5. Edit `.env` files through a key-value interface.
6. Deploy, pull, build, restart, stop, start, and roll back projects with buttons.
7. View project, deployment, Docker, Compose, systemd, Nginx, and panel logs.
8. Connect domains and enable SSL.
9. Create and restore backups.
10. Enable Advanced Mode for carefully controlled VPS administration.

Normal workflows must be visual and click-based: project cards, setup wizards, drag-and-drop uploads, clear status badges, progress steps, confirmations, and useful error messages.

## 3. Required Technology

- Go
- Gin, unless the repository later establishes another Go web framework
- Go HTML templates
- HTMX
- Alpine.js
- Tailwind CSS or lightweight custom CSS
- SQLite
- YAML configuration
- Session-based authentication
- bcrypt or Argon2 password hashing
- Structured application logs
- WebSockets only where live updates are useful
- Docker API when practical; strictly allowlisted Docker CLI actions as fallback
- Internal Go worker for deployment jobs

Avoid React unless an existing implementation later requires it. Avoid unnecessary distributed architecture.

## 4. Proposed Repository Layout

```text
.
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── alerts/
│   ├── audit/
│   ├── auth/
│   ├── backups/
│   ├── compose/
│   ├── config/
│   ├── cron/
│   ├── dashboard/
│   ├── database/
│   ├── deployments/
│   ├── docker/
│   ├── domains/
│   ├── env/
│   ├── files/
│   ├── firewall/
│   ├── installer/
│   ├── logs/
│   ├── network/
│   ├── nginx/
│   ├── processes/
│   ├── projects/
│   ├── storage/
│   ├── systemd/
│   ├── terminal/
│   └── users/
├── web/
│   ├── components/
│   ├── static/
│   └── templates/
├── configs/
│   ├── config.example.yaml
│   ├── config.yaml
│   └── projects.yaml
├── data/
│   └── vpsdeck.db
├── scripts/
│   ├── install.sh
│   ├── uninstall.sh
│   └── vpsdeck.service
├── docs/
│   ├── API.md
│   ├── ARCHITECTURE.md
│   ├── DATABASE_SCHEMA.md
│   ├── INSTALL.md
│   ├── README.md
│   ├── ROADMAP.md
│   ├── SECURITY.md
│   └── TEST_CHECKLIST.md
├── go.mod
└── VPSDECK_PROJECT_CONTEXT.md
```

The layout can evolve, but core deployment logic belongs in `internal/deployments` and Docker Compose logic belongs in `internal/compose`.

## 5. Access Modes

### Simple Mode

Default mode. It exposes only safe, beginner-friendly features:

- Dashboard
- Projects
- Add Project
- Deployments
- Project-scoped Files
- Domains
- Logs
- Backups
- Settings

### Advanced Mode

Advanced Mode may expose:

- Full filesystem access
- Docker and Docker Compose management
- systemd services
- Processes
- Nginx configuration
- Firewall
- Cron jobs
- Databases
- SSH keys
- Optional web terminal
- Security tools

Requirements:

- Admin password confirmation
- Optional second password from configuration
- Audit log entry
- Automatic timeout after inactivity
- Additional confirmations for dangerous actions

## 6. Non-Negotiable Security Rules

1. Require authentication everywhere except the login page and narrowly scoped webhook endpoints.
2. Use HTTPS in production.
3. Hash passwords; never store plaintext credentials.
4. Rate-limit login and webhook endpoints.
5. Use secure, HttpOnly, SameSite session cookies; use `Secure` in HTTPS environments.
6. Require CSRF protection for state-changing browser requests.
7. Audit sensitive actions with user, IP, target, timestamp, result, and error.
8. Validate and canonicalize every filesystem path.
9. Block traversal and symlink escapes outside permitted roots.
10. Restrict Simple Mode file operations to registered project roots.
11. Do not expose arbitrary shell command execution through normal UI or API.
12. Implement operations as allowlisted typed functions such as `RestartProject`, `DeployProject`, `ComposeUp`, `ComposeDown`, `StartContainer`, `ReloadNginx`, and `BackupProject`.
13. Disable the web terminal by default.
14. Require Advanced Mode for full filesystem access and destructive infrastructure actions.
15. Never delete Docker volumes during normal Compose shutdown.
16. Make `docker compose down -v` a separately named dangerous action with explicit confirmation.
17. Never force-reset Git automatically.
18. Require serious confirmation before disabling the firewall or restoring a backup.
19. Back up sensitive files before editing them where practical.
20. Do not create a generic function equivalent to `RunAnyCommand(userInput string)`.

## 7. Main Product Areas

### Authentication

- Login and logout
- One admin user for MVP
- Hashed admin password
- Server-side secure sessions
- Login throttling
- Failed-login audit events
- Advanced Mode confirmation
- Future 2FA placeholder only; do not delay MVP for full 2FA

### Dashboard

Display:

- CPU usage
- RAM used and total
- Storage used and total
- Network upload/download
- Uptime
- OS and kernel versions
- Public and local IP
- Load average
- Running, stopped, and failed project counts
- Docker, Nginx, and database status
- Latest alerts and deployments

Status vocabulary should include Healthy, Warning, Critical, Running, Stopped, Failed, Deploying, and Updating.

### Project Manager

Support these project types:

- Docker
- Docker Compose
- Node.js
- Python
- Go
- Static website
- PM2 app
- systemd service
- Carefully configured custom process

Each project should track its name, type, status, domain, local port, working directory, resource use, disk use, deployment timestamps, restart timestamp, and health-check result.

Project actions:

- Open Website
- Files
- Upload
- Logs
- Env
- Deploy
- Pull Latest
- Restart
- Stop
- Start
- Backup
- Settings
- Delete with confirmation

### Add Project Wizard

Input methods:

- Git repository
- ZIP upload
- File upload
- Docker Compose
- Static website
- Existing folder
- Blank project

Auto-detection:

- `package.json` → Node.js
- `requirements.txt` or `pyproject.toml` → Python
- `go.mod` → Go
- `Dockerfile` → Docker
- `docker-compose.yml`, `compose.yml`, or `compose.yaml` → Docker Compose
- `index.html` → static website

Suggested commands must be generated from project type. Editing command fields should be hidden unless Advanced Mode is active.

### File Manager

Required:

- Breadcrumb browsing
- Project-root restrictions in Simple Mode
- Drag-and-drop upload
- File and folder upload where supported
- ZIP upload and extraction
- File and zipped-folder download
- Create file/folder
- Rename, move, copy, and confirmed delete
- Text editing
- Image/text preview
- Search
- File size and modified date
- Useful icons and optional context menu

Editable text types include `.env`, `.json`, `.yaml`, `.yml`, `.toml`, `.conf`, `.service`, `.sh`, `.js`, `.ts`, `.py`, `.go`, `.html`, `.css`, `.sql`, and `.md`.

### Environment Editor

- Parse `.env` into a key/value table
- Add, edit, and remove variables
- Mask secrets by default
- Show/hide individual values
- Preserve comments and ordering where practical
- Back up the old file before save
- Offer to restart the project after saving

### Logs Center

Combine project, deployment, Docker, Compose, systemd journal, Nginx access/error, app file, and panel logs.

Features:

- Live tail where supported
- Search
- Severity filters
- Pause
- Download
- Copy error
- Clear only when supported and confirmed

## 8. Deployment Engine

Deployment is a core module, not a raw command form.

Supported deployment types:

1. Git project
2. Docker Compose project
3. Dockerfile project
4. Node.js project
5. Python project
6. Go project
7. Static website
8. Existing-folder project
9. Custom allowlisted deployment pipeline

Each project can store:

- Git URL and branch
- Working, release, current, and persistent/shared paths
- Environment file path
- Install, build, migration, start, stop, and restart configuration
- Health-check URL, expected status, timeout, retries, and delay
- Deployment strategy and rollback setting
- Auto-deploy toggle and webhook secret
- Compose file, project name, environment file, and selected services

### Strategy A: Simple Git Pull

1. Acquire per-project deployment lock.
2. Verify repository and branch.
3. Save current commit.
4. Inspect working-tree changes.
5. Stop by default if local changes exist.
6. Fetch and show remote state.
7. Pull only after validation.
8. Run configured install/build/migration steps.
9. Restart with an allowlisted project action.
10. Run health checks.
11. Mark success or offer rollback.
12. Release the lock.

If local changes exist, allow Cancel, Stash, or explicitly confirmed Force Reset. Never reset automatically.

### Strategy B: Release Directories

Preferred production pattern:

```text
/opt/apps/myapp/
├── repo/
├── releases/
├── shared/
│   ├── .env
│   ├── uploads/
│   └── data/
└── current -> releases/<release-id>
```

Create a new release, link shared files, build, migrate, switch the symlink, restart, health-check, and automatically return to the previous symlink if the health check fails. Keep a configurable number of old releases.

### Strategy C: Docker Compose

Safe operations:

- Up
- Down without volumes
- Restart
- Pull
- Build
- Logs
- Status

Deployment flow:

1. Lock project.
2. Validate the Compose file with `docker compose config`.
3. Save Git state when applicable.
4. Pull Git and/or images when configured.
5. Build when configured.
6. Run `up -d`, optionally with `--remove-orphans`.
7. Wait for running/healthy containers.
8. Run the project health check.
9. Save status and logs.
10. Release lock.

All paths and options must be constructed by the backend. Never accept an arbitrary Docker command.

### Strategy D: Dockerfile

Build an image tagged by release/commit, replace the old container using predefined ports/env/volumes, run health checks, and restore the previous image/container on failure when possible.

### Strategy E: Static Website

Pull or upload, optionally build, copy the output to the configured web root, test Nginx, reload only after a successful test, and verify the domain.

### Queue and Live Progress

- Only one deployment per project at a time.
- Default queue behavior: keep the latest pending deployment only.
- Other modes: reject while running or queue all.
- States: Queued, Running, Success, Failed, Rolled Back, Cancelled.
- Store deployment and per-step records.
- Stream progress/logs to the browser.
- Run long work in a background worker.

Typical steps:

- Locking project
- Checking Git status
- Fetching/pulling
- Installing dependencies
- Building
- Running migrations
- Pulling/building images
- Starting containers/service
- Waiting for health check
- Testing domain
- Saving logs
- Finished

### Auto-Deploy

Support GitHub, GitLab, and generic webhooks:

- Verify signature or project secret.
- Verify repository and allowed branch.
- Rate-limit requests.
- Store webhook event.
- Enqueue work and respond quickly.
- Never accept commands from webhook payloads.
- Optionally send Telegram success/failure alerts.

### Rollback and Persistence

- Git pull: retain previous commit and allow confirmed checkout/restart.
- Release strategy: switch back to the previous symlink.
- Docker: retain commit-tagged prior images where practical.
- Warn that database migrations may not be reversible.
- Keep `.env`, uploads, and data outside disposable release directories.
- Encourage named volumes or external bind mounts for Docker.

## 9. Later Infrastructure Modules

These are required eventually but should not delay a working Phase 1 and Phase 2:

- Docker containers, images, volumes, networks, stats, logs, and Compose projects
- systemd service controls and journal logs
- Process list and confirmed process termination
- Nginx domain/reverse-proxy management
- Certbot SSL issuance, renewal, and expiry display
- PostgreSQL, MySQL/MariaDB, and Redis service/backup basics
- Project, database, Docker volume, and panel backups
- Storage analysis and confirmed cleanup
- Open ports, listeners, domain mappings, firewall, and Docker port view
- UFW management in Advanced Mode
- Friendly cron scheduler
- SSH authorized-key management
- Telegram alerts
- Panel version, logs, restart, update, config/database backup, import/export
- Optional PTY-over-WebSocket terminal in a later phase only

The terminal must remain disabled by default, require Advanced Mode, a second password, an IP allowlist, auditing, and inactivity timeout.

## 10. Core Data Model

SQLite tables planned for the initial architecture:

- `users`: username, password hash, role, timestamps
- `sessions`: session ID, user ID, expiry, timestamps
- `projects`: identity, type, domain/port, working directory, status, commands, health check, env/log/service/Docker references
- `deployment_settings`: strategy, Git, command, health-check, Compose, queue, release and rollback settings
- `deployments`: project, trigger, state, commit/release before and after, timing, error
- `deployment_steps`: named step, state, output/error, timing
- `webhook_events`: provider, event, branch, commit, source IP, status/error
- `domains`: domain, project, target port, Nginx config, SSL state/expiry
- `backups`: type, target, path, size, state, timestamp
- `audit_logs`: user, IP, action, target, details, success/error, timestamp
- `settings`: key/value configuration overrides
- `alerts`: level, title, message, source, acknowledgment, timestamp

Use migrations rather than creating schema ad hoc in handlers.

## 11. Route Families

Planned route groups:

- `/login`, `/logout`
- `/advanced-mode/enable`, `/advanced-mode/disable`
- `/dashboard` and `/api/dashboard/*`
- `/projects`, `/projects/new`, `/projects/:id/*`
- `/projects/:id/deployments/*`
- `/projects/:id/compose/*`
- `/webhooks/deploy/:project_id/:webhook_token`
- `/files/*`
- `/projects/:id/env`
- `/logs` and WebSocket log endpoints
- `/docker/*`
- `/domains/*`
- `/services/*`
- `/backups/*`
- `/audit`
- `/terminal` and `/ws/terminal` only when explicitly enabled and authorized

Handlers must call typed services. They must not concatenate request values into shell commands.

## 12. UI Rules

Simple Mode sidebar:

```text
Dashboard
Projects
Add Project
Deployments
Files
Domains
Logs
Backups
Settings
```

Advanced Mode adds Docker, Services, Databases, Storage, Network, Firewall, Cron Jobs, Terminal, and Security.

Use plain labels:

- “Deploy Now,” not “Execute deployment pipeline”
- “Start App,” not `docker compose up -d`
- “Stop App,” not `docker compose down`

Advanced Mode may show a read-only command preview. Every long operation needs visible progress, and every page must either work or clearly say “Coming Soon.”

## 13. Delivery Phases

### Phase 1: Core MVP

- [x] Bootstrap Go module, Gin server, config loading, structured logging, and graceful shutdown.
- [x] Add SQLite connection and migrations.
- [x] Add admin setup/bootstrap flow.
- [x] Add login/logout, secure sessions, CSRF, and login rate limiting.
- [x] Add base HTML layout, navigation, and responsive styling.
- [x] Add dashboard CPU/RAM/storage/uptime cards.
- [ ] Add project CRUD.
- [x] Add existing-folder project registration.
- [x] Add project cards and detail view.
- [x] Add project-scoped file manager.
- [ ] Add file upload and ZIP upload/extraction. (Regular upload works; safe ZIP extraction remains.)
- [x] Add text-file editor with path safety and backups.
- [x] Add `.env` key-value editor.
- [ ] Add basic project logs viewer.
- [ ] Add safe configured start/stop/restart actions.
- [ ] Add audit logging and audit page.
- [ ] Add Phase 1 tests and complete its acceptance checks.

Do not build the web terminal in Phase 1.

### Phase 2: Deployment Engine and Docker Compose

- [ ] Add project deployment settings.
- [ ] Add per-project deployment lock and queue worker.
- [ ] Add safe Git pull deployment.
- [ ] Add one-click deploy and progress UI.
- [ ] Persist deployment history, steps, and logs.
- [ ] Stream live progress/logs.
- [ ] Add Compose config validation.
- [ ] Add Compose up/down/restart/pull/build/logs/status.
- [ ] Add Compose deployment flow.
- [ ] Add configurable health checks.
- [ ] Add basic rollback.
- [ ] Add secret-verified auto-deploy webhook.
- [ ] Add optional Telegram deploy alerts.
- [ ] Audit every deployment and Compose action.
- [ ] Complete Phase 2 acceptance checks.

Do not build Kubernetes, a full terminal, arbitrary public command execution, or default volume deletion.

### Phase 3: Domains and VPS Tools

- [ ] Nginx domain manager
- [ ] SSL manager
- [ ] systemd service manager
- [ ] Process manager
- [ ] Network/ports page
- [ ] Storage manager

### Phase 4: Advanced VPS Access

- [ ] Advanced full-filesystem mode
- [ ] UFW manager
- [ ] Cron manager
- [ ] SSH key manager
- [ ] Optional guarded web terminal

### Phase 5: Reliability

- [ ] Backup and restore manager
- [ ] Wider Telegram alerts
- [ ] Panel self-update
- [ ] Multi-user roles
- [ ] Historical resource charts

## 14. First Useful Version Acceptance Test

The first useful version is accepted when an administrator can:

- [ ] Install VPSDeck on a VPS.
- [ ] Open it in a browser and log in.
- [ ] See CPU, RAM, storage, and uptime.
- [ ] Add an existing project.
- [ ] Upload and edit project files.
- [ ] Edit `.env`.
- [ ] View logs.
- [ ] Restart a project.
- [ ] See the action in audit logs.
- [ ] Add a Git project.
- [ ] Pull latest code.
- [ ] Deploy and see live progress/logs.
- [ ] Roll back a failed deployment.
- [ ] View Docker containers when Docker is available.
- [ ] Run Compose up/down/restart.
- [ ] View Compose logs.
- [ ] Trigger an authenticated auto-deploy webhook.

## 15. VPS Inspection Checklist

Before making deployment or installer assumptions on a real VPS, inspect:

- OS and architecture
- Existing repository files
- Go version
- Docker daemon and client
- Docker Compose plugin
- Nginx
- Certbot
- systemd
- PM2
- Node.js
- Python
- PostgreSQL
- MySQL/MariaDB
- Redis
- Listening ports
- Existing projects under `/var/www`, `/opt`, `/srv`, `/home`, `/home/apps`, and `/opt/apps`

Inspection must be read-only unless the user authorizes installation or system changes.

## 16. Current Working State

Last updated: **2026-06-22**

Completed:

- [x] Read and consolidated the full VPSDeck build specification.
- [x] Confirmed the repository was empty before implementation.
- [x] Created this persistent context and continuation document.
- [x] Bootstrapped a Go 1.25 application using Gin, YAML config, structured logging, embedded templates/static assets, and graceful shutdown.
- [x] Added SQLite migrations for users, sessions, projects, and audit logs using the pure-Go `modernc.org/sqlite` driver.
- [x] Added first-admin environment bootstrap with bcrypt password policy.
- [x] Added server-side hashed session tokens, secure cookie settings, CSRF protection, login rate limiting, security headers, no-store responses, and login auditing.
- [x] Added a responsive Simple Mode UI and live CPU, RAM, disk, uptime, OS, kernel, local-IP, and project-count dashboard.
- [x] Added existing-folder project registration, automatic type detection, project cards/details, and unregister-without-file-deletion behavior.
- [x] Added canonical allowed-root checks and symlink-escape protection.
- [x] Added project-scoped file browsing, regular file upload, folder creation, file download, safe deletion, and text editing with automatic backups.
- [x] Added a key-value `.env` editor with secret masking and backup-before-save.
- [x] Added an audit page and audit events for authentication, project, file, and environment actions.
- [x] Added automated tests for auth/session/CSRF, project root controls, symlink escapes, file backups, HTTP login, project registration, file editing, environment editing, and audit records.
- [x] Completed a live HTTP smoke test on port 18080: health, login, dashboard, project registration, project detail, and audit page all passed.
- [x] Started the persistent local workspace instance on `127.0.0.1:8080` and initialized the `admin` account.
- [x] Re-ran the full automated suite successfully with `go test ./...`.
- [x] Completed a live end-to-end test against the real workspace database: authentication, dashboard, project registration, `.env` save, folder creation, file upload/download, text edit with backup, audit verification, unauthenticated redirect, and CSRF rejection all passed.
- [x] Fixed the main sidebar Files item, which was still a disabled placeholder; `/files` now lists registered projects and links directly into each safe project-scoped file manager.
- [x] Replaced manual project-folder typing with an approved-root browser using breadcrumbs, hidden-folder control, and server-side traversal/symlink containment.
- [x] Added cached TCP and UDP port monitoring with process metadata, bind exposure labels, project matching, and warnings for configured project ports that are not listening.
- [x] Added configurable local/remote Ollama monitoring using `/api/version`, `/api/tags`, and `/api/ps`, including online/degraded/offline/disabled states.
- [x] Added read-only official Linux and Docker installation guidance when Ollama is unavailable.
- [x] Added authenticated monitor JSON APIs, dedicated Ports/Ollama pages, sidebar links, dashboard summaries, and five-second browser polling.
- [x] Verified the monitor slice with unit/integration tests, race detector, vet, JavaScript syntax check, production build, and a live HTTP smoke test on port 18081.
- [x] Live monitor smoke results: 14 TCP listeners, 9 UDP sockets, VPSDeck detected on 8080, project port 9090 reported inactive, folder picker found `smoke-app`, and Ollama correctly reported not detected.
- [x] Added Docker Compose auto-discovery using allowlisted Docker CLI commands and daemon-provided Compose metadata.
- [x] Added explicit import of detected Compose stacks, including trusted working directory, published host port, container/service details, and runtime status synchronization.
- [x] Imported the live `aigenius-full` stack: 6/6 containers running, healthy web service, localhost port 80, working directory `/Users/cravemac2/aigenius-full`.
- [x] Port 80 now associates with `aigenius-full` instead of appearing only as the Docker Desktop backend process.
- [x] Detected the stopped `qparking` Compose stack without importing it.

In progress:

- VPSDeck is running with Docker Compose discovery on `http://127.0.0.1:8080` (PID 30461).

Next action:

1. Add safe ZIP upload/extraction with zip-slip, symlink, entry-count, and expanded-size protections.
2. Add basic project log source configuration/viewing.
3. Define allowlisted project runtime adapters before adding start/stop/restart buttons.

Known blockers:

- None.

Decisions made:

- Use lightweight custom CSS to keep installation dependency-free.
- Use pure-Go `modernc.org/sqlite`.
- Store SHA-256 hashes of opaque random session IDs in SQLite; only the raw token reaches the HttpOnly cookie.
- Bootstrap the first administrator from `VPSDECK_ADMIN_USERNAME` and `VPSDECK_ADMIN_PASSWORD`.
- Restrict local Simple Mode projects to `./managed-apps` by default.
- Reject upload overwrites; require an explicit edit flow for existing text files.
- Delete files and empty folders only. Recursive folder deletion is not yet exposed.
- Monitor only one configured Ollama endpoint at a time; use `VPSDECK_OLLAMA_BASE_URL` for environment-specific overrides.
- Keep Ollama installation guidance read-only. VPSDeck never executes the displayed installation commands.
- Cache port and Ollama snapshots for `monitoring.refresh_seconds`.
- Treat Docker daemon Compose metadata as a trusted discovery source; import requires an exact detected project name and never accepts a user-supplied filesystem path or Docker command.
- Imported Docker Compose projects may live outside `simple_mode_roots`; their file access is allowed because the path comes from Docker's Compose working-directory label.

Decisions still open:

- Runtime adapter model for safe project start/stop/restart actions.
- Whether ZIP extraction should reject an entire archive when any destination already exists or offer an explicit overwrite workflow later.

## 17. Session Continuation Protocol

Every development session must:

1. Read this file first.
2. Inspect actual repository state and `git status`; do not trust this document blindly if code differs.
3. Continue from the first relevant unchecked item.
4. Implement a small working vertical slice.
5. Run proportionate tests and smoke checks.
6. Update this file before ending:
   - Change “Last updated.”
   - Mark completed checklist items.
   - Record exactly what was implemented.
   - Record commands used to run/test it.
   - Record errors, blockers, and important decisions.
   - Set one concrete “Next action.”

Keep status entries concise. Do not paste routine logs into this file.

## 18. Run and Test Commands

```bash
go mod download
VPSDECK_ADMIN_USERNAME=admin \
VPSDECK_ADMIN_PASSWORD='ChangeThisStrong123' \
go run ./cmd/server

go test ./...
go vet ./...
go build ./cmd/server
```

Open `http://127.0.0.1:8080`. First-admin environment variables are needed only while the users table is empty.

Current local configuration:

- Database: `./data/vpsdeck.db`
- Managed project root: `./managed-apps`
- Edit backups: `./data/backups/file-edits`
- Bind address: `127.0.0.1:8080`
- Local admin username: `admin`
- Live test data: one registered project named `Live Smoke App`
- Current verified database state: 1 user, 2 projects (`Live Smoke App` and running `aigenius-full`)
- Monitor refresh: 5 seconds
- Ollama endpoint: `http://127.0.0.1:11434`
- Monitor pages: `/network/ports` and `/ollama`
- Docker Compose discovery: enabled with the `docker` CLI and a 10-second command timeout

## 19. Implementation Guardrails

- Build working vertical slices; do not create a forest of empty placeholder pages.
- Preserve existing working code.
- Do not delete user data or existing projects.
- Validate capability availability and report unsupported services cleanly.
- Hide unavailable or dangerous controls rather than letting them fail mysteriously.
- Prefer backend service interfaces so Linux integrations can be tested with fakes.
- Use contexts and timeouts for subprocesses and external operations.
- Capture stdout/stderr safely without leaking secrets into logs.
- Redact credentials, tokens, cookies, and `.env` secret values.
- Confirm destructive actions in both UI and backend authorization.
- Test path containment, CSRF, session expiry, login throttling, and command argument handling.
- Keep VPSDeck usable without Docker; Docker features should degrade gracefully.
- Start with Phase 1, then build Phase 2. Do not attempt all advanced modules in one huge change.

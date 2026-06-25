# Installing VPSDeck on Ubuntu

The production installer keeps VPSDeck private on `127.0.0.1:7788` by default, or the next free localhost port, and publishes it through an existing Nginx server. It does not replace other Nginx sites.

## Requirements

- Ubuntu 22.04 or newer
- Root SSH access
- A domain whose DNS can point to the VPS
- TCP ports 80 and 443 open

Docker is optional, but Docker Compose project discovery is enabled automatically when Docker is present.

## Install

Connect to the server and run:

```bash
git clone https://github.com/rexzai1992/Vps-Deck.git /tmp/vpsdeck-installer
sudo bash /tmp/vpsdeck-installer/scripts/install.sh
```

The installer asks for the first VPSDeck administrator username and password. The password must contain uppercase, lowercase, and numeric characters and be at least 12 characters. It is hashed immediately and is not saved in a shell configuration file.

To use another hostname:

```bash
sudo VPSDECK_DOMAIN=panel.example.com bash /tmp/vpsdeck-installer/scripts/install.sh
```

## DNS and HTTPS

Create this DNS record:

```text
Type: A
Name: vps
Value: your VPS public IPv4 address
```

After the hostname resolves to the VPS, enable HTTPS:

```bash
sudo certbot --nginx -d vps.izzul.xyz --redirect
```

VPSDeck uses secure cookies in production, so sign-in should be done through the HTTPS hostname rather than the server IP.

## Service management

```bash
sudo systemctl status vpsdeck
sudo systemctl restart vpsdeck
sudo journalctl -u vpsdeck -f
curl http://127.0.0.1:7788/healthz
```

The important paths are:

```text
/usr/local/bin/vpsdeck          application binary
/etc/vpsdeck/config.yaml        production configuration
/var/lib/vpsdeck/vpsdeck.db     users, sessions, projects, and audit data
/var/backups/vpsdeck            binary and file backups
/opt/vpsdeck/src                installed source checkout
```

## Reverse proxy manager

Nginx is the only public entrypoint for normal web traffic on ports 80 and 443. VPSDeck and registered projects should listen on localhost/internal ports, then the **Domains** page creates managed Nginx reverse proxy routes for public hostnames.

The installer creates one bridge file that Nginx already reads:

```text
/etc/nginx/sites-enabled/vpsdeck-managed.conf
```

That bridge includes only VPSDeck's managed route directory:

```text
include /etc/nginx/deploynest/sites-enabled/*.conf;
```

Generated route files live here:

```text
/etc/nginx/deploynest/sites-available
/etc/nginx/deploynest/sites-enabled
```

VPSDeck does not edit `/etc/nginx/sites-enabled/default` or unrelated user/application configs. Every route change writes a candidate config, updates the managed symlink, runs `nginx -t`, and reloads Nginx only after the test passes. If the test fails, the candidate file/symlink is rolled back and the error is stored on the route.

Project routes prefer the internal port pool `31000-31999` and skip ports already assigned to projects, assigned to proxy routes, occupied on localhost, or used by the panel. A **Target port is not listening** warning means Nginx may be configured correctly, but the app behind that hostname is not currently accepting connections on its target localhost port.

## Update and rollback

VPSDeck checks its own GitHub branch for new commits and shows an **Updates** page (and a dashboard banner) when a newer version is available. Press **Update now** to apply it from the browser.

Because the panel runs as the unprivileged `vpsdeck` user, it cannot rebuild or restart itself directly. Instead, pressing **Update now** writes a request flag to `/var/lib/vpsdeck/update.request`. A root-owned, path-activated systemd unit watches that flag and runs the updater:

```text
/etc/systemd/system/vpsdeck-update.path      watches the request flag
/etc/systemd/system/vpsdeck-update.service   runs update.sh as root
/var/lib/vpsdeck/update.request              update request flag (written by the panel)
/var/lib/vpsdeck/update.status               progress/result the panel polls
```

These units are installed and enabled automatically by `scripts/install.sh`. **Existing installations must re-run the installer once** (or `sudo systemctl enable --now vpsdeck-update.path` after copying the two unit files) before browser updates work.

The installer also runs `git config --system --add safe.directory /opt/vpsdeck/src` so the unprivileged `vpsdeck` user can read the root-owned checkout's revision for update detection. If the Updates page shows "Could not read the installed revision" on an older install, run that command once.

You can still update from the shell at any time:

```bash
sudo /opt/vpsdeck/src/scripts/update.sh
```

Either way, the updater fetches the configured branch, runs tests, builds a new binary, restarts the service, and checks `/healthz`. If the health check fails, it restores the previous source revision and binary, and records the failure on the Updates page.

## Connect a GitHub account (OAuth App)

To let administrators browse and deploy their repositories (including private ones) from the panel, register a GitHub OAuth App and give VPSDeck its credentials.

1. On GitHub: **Settings → Developer settings → OAuth Apps → New OAuth App**.
   - Homepage URL: `https://vps.izzul.xyz`
   - Authorization callback URL: `https://vps.izzul.xyz/integrations/github/callback`
2. On the server, run the helper and paste the Client ID and Secret when prompted:

```bash
sudo /opt/vpsdeck/src/scripts/enable-github.sh
```

It generates the AES token key, writes `/etc/vpsdeck/github.env` (mode 0640, `root:vpsdeck`), and restarts VPSDeck. The installer already added `EnvironmentFile=-/etc/vpsdeck/github.env` to the service unit and created a disabled template, so the only manual values are the two OAuth credentials. To set them by hand instead, edit `/etc/vpsdeck/github.env`, set `VPSDECK_GITHUB_ENABLED=true`, and `sudo systemctl restart vpsdeck`.

For local development, register an OAuth App with callback `http://127.0.0.1:8080/integrations/github/callback` and export the same `VPSDECK_GITHUB_*` variables before `go run ./cmd/server`.

The OAuth access token is encrypted at rest with `VPSDECK_GITHUB_TOKEN_KEY` (AES-256-GCM) and is supplied to git through a temporary credential helper, never via the repository URL or command line. Keep the key stable; rotating it invalidates stored tokens and administrators must reconnect.

## Uninstall

Remove the application while preserving its configuration and data:

```bash
sudo /opt/vpsdeck/src/scripts/uninstall.sh
```

To also request deletion of configuration, database, logs, and backups:

```bash
sudo /opt/vpsdeck/src/scripts/uninstall.sh --purge
```

## Permissions and Docker

VPSDeck runs as the dedicated `vpsdeck` system user. The installer adds that user to the `docker` group when Docker is installed. Membership in the Docker group is effectively root-level access, so only trusted VPSDeck administrators should have panel access.

Project folders must be readable by `vpsdeck`; file editing also requires write permission. For an app managed under `/opt/apps`, a typical ownership command is:

```bash
sudo chown -R vpsdeck:vpsdeck /opt/apps/my-app
```

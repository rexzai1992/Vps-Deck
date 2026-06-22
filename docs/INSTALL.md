# Installing VPSDeck on Ubuntu

The production installer keeps VPSDeck private on `127.0.0.1:8080` and publishes it through an existing Nginx server. It does not replace other Nginx sites.

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
curl http://127.0.0.1:8080/healthz
```

The important paths are:

```text
/usr/local/bin/vpsdeck          application binary
/etc/vpsdeck/config.yaml        production configuration
/var/lib/vpsdeck/vpsdeck.db     users, sessions, projects, and audit data
/var/backups/vpsdeck            binary and file backups
/opt/vpsdeck/src                installed source checkout
```

## Update and rollback

Run:

```bash
sudo /opt/vpsdeck/src/scripts/update.sh
```

The updater fetches `main`, runs tests, builds a new binary, restarts the service, and checks `/healthz`. If the health check fails, it restores the previous source revision and binary.

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

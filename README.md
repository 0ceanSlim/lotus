# 🪷 Lotus

A [Blossom](https://github.com/hzrd149/blossom) media server with a built-in web frontend. Upload, browse, and manage your media files using Nostr keypairs for authentication.

## Features

- Full BUD-01/02/04/06/08 protocol support (get, upload, list, mirror, requirements, NIP-94 metadata)
- Web gallery frontend served from the data directory — swap it out per instance without recompiling
- Access control rules per pubkey and resource (UPLOAD, GET, DELETE, LIST, MIRROR)
- Per-pubkey storage quotas
- MIME type allow/deny list
- Auto-fetch allowed pubkeys from a `.well-known/nostr.json` endpoint
- Optional [0x0.st](https://0x0.st) ephemeral storage backend — same binary, config-driven
- Embedded SQLite migrations — no migration tooling needed
- Single compiled binary, data-directory deployment model

## Quick Start

### 1. Build

```sh
git clone https://github.com/0ceanSlim/lotus
cd lotus
go build -o bin/lotus ./cmd/lotus/
```

### 2. Create a data directory

```sh
mkdir -p ~/.blossom/web
```

Copy or symlink your frontend files into `~/.blossom/web/`. The server expects `web/scripts/`, `web/res/`, and HTML templates inside that directory.

### 3. Write a config

Create `~/.blossom/config.yml`:

```yaml
db_path: "db/database.sqlite3"
log_level: "INFO"
api_addr: "0.0.0.0:8484"
cdn_url: "https://your.domain.com"
admin_pubkey: "<your-hex-pubkey>"

max_upload_size_bytes: 104857600       # 100 MB
max_storage_per_pubkey_bytes: 8589934592  # 8 GB

# Optional: auto-fetch allowed uploaders from a nostr.json
# nostr_users_url: "https://your.domain.com/.well-known/nostr.json"

access_control_rules:
  - action: "ALLOW"
    pubkey: "<your-hex-pubkey>"
    resource: "UPLOAD"
  - action: "ALLOW"
    pubkey: "ALL"
    resource: "GET"

allowed_mime_types:
  - "*"
```

### 4. Run

```sh
./bin/lotus --data-dir ~/.blossom
```

The data directory can also be set via the `BLOSSOM_DATA_DIR` environment variable. If neither is provided it defaults to `~/.blossom`.

### Running as a systemd service

```ini
[Unit]
Description=Lotus Blossom Server
After=network.target

[Service]
Type=simple
User=youruser
ExecStart=/path/to/lotus --data-dir /path/to/data-dir
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

## 0x0 Storage Backend

Lotus can use a self-hosted [0x0](https://git.0x0.st/mia/0x0) instance as its storage backend. Files are uploaded to your 0x0 instance and proxied back through Lotus — no local disk storage required on the Lotus side.

Retention policy and file size limits are configured in the 0x0 backend itself. The matching values in Lotus's config are used only to mirror the 0x0 retention curve locally, so Lotus knows when to expire records from its own database.

```yaml
zero_x_zero:
  enabled: true
  instance_url: "https://your-0x0-instance.example.com"
  # Mirror these values from your 0x0 backend config so Lotus
  # can accurately calculate local expiry for database cleanup.
  max_file_size_bytes: 524288000   # match 0x0's MAX_CONTENT_LENGTH
  min_retention_days: 30           # match 0x0's MIN_RETENTION
  max_retention_days: 365          # match 0x0's MAX_RETENTION
```

The same binary serves both modes. Run two instances pointing at different data directories to serve a standard Blossom server and a 0x0-backed server simultaneously.

## Data Directory Layout

```
data-dir/
├── config.yml        # server config
├── db/
│   └── database.sqlite3
└── web/              # frontend assets (not bundled in binary)
    ├── scripts/
    ├── res/
    └── *.html
```

## Configuration Reference

| Key | Description |
|-----|-------------|
| `db_path` | Path to SQLite database. Relative paths are resolved from the data directory. |
| `log_level` | `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `api_addr` | Address and port to listen on |
| `cdn_url` | Base URL used to construct blob URLs in responses |
| `admin_pubkey` | Hex pubkey with admin privileges |
| `nostr_users_url` | URL of a `.well-known/nostr.json` to auto-fetch allowed uploaders (refreshed every 5 min) |
| `max_upload_size_bytes` | Per-upload size limit |
| `max_storage_per_pubkey_bytes` | Total storage quota per pubkey (`0` = unlimited) |
| `access_control_rules` | List of ALLOW/DENY rules by pubkey and resource |
| `allowed_mime_types` | List of accepted MIME types (`*` = any) |
| `zero_x_zero` | 0x0.st backend config block (see above) |

## Roadmap

- **Gallery improvements** — filtering, search, pagination in the web UI
- **Upload UI** — drag-and-drop upload interface in the frontend
- **Performance** — response caching, connection pooling, range request optimizations
- **Outbox model** — once the [Grain](https://github.com/0ceanSlim/grain) client library matures, integrate the Nostr outbox model for relay-aware profile and content resolution
- **Admin UI** — pubkey management, ACR editor, storage stats dashboard in the frontend

## Lineage

Lotus is a fork of [sebdeveloper6952/blossom-server](https://github.com/sebdeveloper6952/blossom-server), which provided the original BUD protocol backend implementation. The original project laid the foundation for the blob storage, access control, and Nostr authentication layers that Lotus builds on.

Lotus diverged to add a decoupled web frontend, the data-directory deployment model, embedded migrations, 0x0.st backend support, and BUD-08 NIP-94 metadata — while tracking upstream bug fixes and protocol improvements where they apply.

## License

See [LICENSE](LICENSE).

# 🪷 Lotus

A [Blossom](https://github.com/hzrd149/blossom) media server with a personal drive on top. Nostr clients upload to it like any other Blossom server; the drive frontend is where you find, organize, share and clean up what landed there.

Built on the [grain](https://github.com/0ceanSlim/grain) Nostr client library and the [mill](https://github.com/0ceanSlim/nostr-mill) signer.

## Features

- Full BUD-01/02/04/06/08 protocol support (get, upload, list, mirror, requirements, NIP-94 metadata)
- **Drive frontend**: sign in with any Nostr signer (extension, Amber, bunker, key), see every blob you have here newest first, filter by type, preview, copy links, upload, and delete with a signed Blossom request
- **Server page**: identity, limits, supported BUDs and totals for visitors, plus a machine-readable `/api/v1/server/info`
- **Challenge-bound sign-in**: a drive session is only minted for a pubkey that proves it holds the key (NIP-98 signed login)
- Profile and relay resolution through grain's outbox-model client; sign-in UI through mill
- Web frontends served from the data directory — swap them per instance without recompiling
- Access control rules per pubkey and resource (UPLOAD, GET, DELETE, LIST, MIRROR)
- Per-pubkey storage quotas
- MIME type allow/deny list
- Auto-fetch allowed pubkeys from a `.well-known/nostr.json` endpoint
- Optional [0x0.st](https://0x0.st) ephemeral storage backend — same binary, config-driven
- Embedded SQLite migrations — no migration tooling needed
- Single compiled binary, data-directory deployment model

## Frontends

The drive frontend is compiled into the binary and served by default. A `web/` folder in the data directory overrides it, which is how the 0x0 deployment runs its own frontend and how you bring your own.

| Frontend | Path | Description |
|----------|------|-------------|
| `drive` | `frontends/drive/` | The main frontend. Server page for visitors, a personal drive for signed-in users. Built on grain's `/api/v1` session API and mill. Actively developed. |
| `0x0` | `frontends/0x0/` | Frontend for ephemeral deployments backed by a self-hosted 0x0 instance, with a public gallery. Uses the legacy `/api/auth` session API, which the server keeps serving unchanged. |

To run a frontend from disk instead of the embedded one, copy it into your data directory:

```sh
cp -r frontends/0x0 /your/data-dir/web
```

You can also bring your own frontend entirely. The server just needs Go template HTML files under `web/views/` (`templates/layout.html`, `templates/header.html`, `templates/footer.html`, `components/*.html`, and one file per view) plus whatever static assets you reference from `web/static/`, `web/scripts/` or `web/res/`. Nothing is hardcoded in the binary.

### The drive

Most Blossom uploads never touch a web UI: a client attaches an image to a post and the blob lands here with no name, no folder and no context. The drive is the management layer over those blobs.

- Blob URLs are the sha256 plus an extension, so anything the drive does to a file never changes a link that is already embedded in a post.
- Sign-in runs through mill. A write session carries a NIP-98 authorization signed by the same key; the server verifies it before minting the session cookie. Read-only sessions can be created unsigned but cannot see or change files.
- Uploads and deletes are ordinary signed Blossom requests (kind 24242), the same ones any client sends.
- Short links: from a file's detail view, create `/s/<code>`, a seven-character alias that redirects to the canonical sha256 URL. Links are per user and per file, can be revoked, and vanish when the file is deleted. The canonical URL is never affected.
- Settings at `/drive/settings` manage the user's Blossom server list (kind 10063): add this server or make it primary, add others by URL, reorder, remove, then sign and publish through grain's outbox routing with a per-relay result toast. The drive shows a one-click "add this server" banner until the server is in the list. The only server lotus suggests is itself.
- The server page at `/` is the dash for everyone: limits, retention, cost, supported BUDs and totals. The drive lives at `/drive` and needs a session; sign-in lands there. There is no public feed of uploads; the all-blobs listing used by gallery frontends can be switched off with `public_listing: false`.

The drive frontend bundles mill, htmx and the Tailwind v4 browser build locally under `static/js`, so it makes no third-party requests and needs no build step to edit.

## Quick Start

### 1. Build

```sh
git clone https://github.com/0ceanSlim/lotus
cd lotus
go build -o bin/lotus ./cmd/lotus/
```

### 2. Run it

```sh
./bin/lotus --data-dir ~/.blossom
```

On first start lotus creates the data directory, writes a commented `config.yml` with defaults, creates the database, and serves the drive frontend compiled into the binary. The server starts **unclaimed**: every page shows a banner pointing at `/setup`, where the first person to sign in with a Nostr signer becomes the operator. Claiming records their pubkey as `admin_pubkey`, grants them upload access, and opens the admin page.

Set `cdn_url` in `config.yml` to the public URL clients will reach the server at, then restart. Everything else can be changed from the admin page.

To skip the claim, set `admin_pubkey` to your hex pubkey in `config.yml` before starting.

### 3. Or write the config yourself

The full config, with the defaults lotus would write:

```yaml
db_path: "db/database.sqlite3"
log_level: "INFO"
api_addr: "0.0.0.0:8484"
cdn_url: "https://your.domain.com"
admin_pubkey: "<your-hex-pubkey>"

max_upload_size_bytes: 104857600       # 100 MB
max_storage_per_pubkey_bytes: 8589934592  # 8 GB

# Drive deployments: no public all-blobs listing.
public_listing: false

# How the server page and the sign-in modal describe this deployment.
server:
  name: "My Blossom"
  description: "Permanent media storage for my nostr friends."
  icon: "https://your.domain.com/icon.png"
  cost: "paid"                         # "free" or "paid"
  membership_url: "https://your.domain.com/join"
  terms_url: "https://your.domain.com/terms"
  privacy_url: "https://your.domain.com/privacy"

# Optional: auto-fetch allowed uploaders from a nostr.json
# nostr_users_url: "https://your.domain.com/.well-known/nostr.json"

# Optional: relays grain uses to resolve profiles and relay lists
# index_relays: ["wss://purplepag.es", "wss://user.kindpag.es"]

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

The data directory can also be set via the `BLOSSOM_DATA_DIR` environment variable. If neither is provided it defaults to `~/.blossom`.

### Admin

`/admin` is available to the operator's session only. It edits identity, cost and membership link, the public listing switch, limits, the nostr.json members URL, access rules and accepted file types, lists users by storage used, and can transfer the operator key. Every save is signed with the operator's key (NIP-98) and applied to the running server immediately; the file is rewritten without its comments. `api_addr`, `cdn_url`, `db_path` and `log_level` are shown read-only and need a restart.

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
    ├── static/       # drive: css, js, mill
    ├── scripts/      # 0x0 and gallery frontends
    ├── res/
    └── views/
```

## HTTP API

Blossom endpoints follow the BUDs: `GET|HEAD /<sha256>`, `PUT /upload`, `HEAD /upload`, `PUT /mirror`, `DELETE /<sha256>`, `GET /list/<pubkey>`, plus `GET /stats`.

The drive frontend talks to `/api/v1/`, which is grain's client API mounted on lotus plus a few lotus endpoints:

| Endpoint | What it does |
|----------|--------------|
| `POST /api/v1/auth/login` | Mint a session. Write mode requires a NIP-98 `Authorization: Nostr <base64 kind-27235>` header signed by the pubkey in the body. |
| `GET /api/v1/session`, `POST /api/v1/auth/logout` | Session state and sign-out. |
| `GET /api/v1/cache`, `GET /api/v1/user/profile` | Profile metadata resolved through grain. |
| `GET /api/v1/user/media-servers`, `POST .../build`, `POST /api/v1/events/publish`, `POST .../publish/stream` | Read and update the user's Blossom server list (kind 10063); the stream variant reports per-relay results as NDJSON. |
| `GET /api/v1/server/info` | This deployment's identity, limits and capabilities. The leading fields use grain's media-server info shape. |
| `GET /api/v1/drive/files` | The session user's blobs, newest first, with storage totals and any short links. |
| `POST /api/v1/drive/files/<sha256>/short`, `DELETE /api/v1/drive/short/<code>` | Create or revoke a short link for a blob you own. `GET /s/<code>` redirects to the blob. |

The legacy `/api/auth/*`, `/api/user/*`, `/api/profile` and `/list-all` endpoints used by the 0x0 frontend are unchanged.

## Local development

```sh
go run ./cmd/lotus --data-dir .dev-data
```

`.dev-data/` is gitignored and lotus creates it on first run. To edit the frontend live, add a `web/` folder inside it that is a symlink or junction to `frontends/drive/`; template, script and CSS edits then show on reload, while Go changes need a restart. Without the link the embedded copy is served, which needs a rebuild to pick up frontend edits.

## Configuration Reference

| Key | Description |
|-----|-------------|
| `db_path` | Path to SQLite database. Relative paths are resolved from the data directory. |
| `log_level` | `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `api_addr` | Address and port to listen on |
| `cdn_url` | Base URL used to construct blob URLs in responses |
| `admin_pubkey` | Hex pubkey of the operator. Empty means unclaimed; the first sign-in at `/setup` fills it. |
| `nostr_users_url` | URL of a `.well-known/nostr.json` to auto-fetch allowed uploaders (refreshed every 5 min) |
| `max_upload_size_bytes` | Per-upload size limit |
| `max_storage_per_pubkey_bytes` | Total storage quota per pubkey (`0` = unlimited) |
| `access_control_rules` | List of ALLOW/DENY rules by pubkey and resource |
| `allowed_mime_types` | List of accepted MIME types (`*` = any) |
| `zero_x_zero` | 0x0.st backend config block (see above) |
| `public_listing` | Serve the unauthenticated all-blobs listing at `/list-all` (default `true`; drive deployments set `false`) |
| `server` | Identity shown on the server page and in the sign-in modal: `name`, `description`, `icon`, `contact`, `cost`, `membership_url`, `terms_url`, `privacy_url`, and `profile_url`, the template every pubkey links through with `{npub}` or `{hex}` replaced (default njump; point it at your own client, e.g. `https://wheat.oslim.dev/p/{npub}`) |
| `index_relays` | Relays grain uses to resolve profiles and relay lists (defaults to grain's built-in seed list) |

## Roadmap

- **Drive organization** — folders as metadata over the flat blob list, names, trash, search, share pages and short links, a per-user files table so identical bytes from two users are both listed
- **Context** — "used in" links from a blob back to the user's notes that embed it, and a delete warning when a link is live
- **Safety and admin** — BUD-09 blob reports, a blocked-hash list enforced on upload and mirror, optional malware scanning, and an admin dashboard gated by session plus NIP-98 signed mutations
- **Nostr integration** — add-this-server to the user's kind 10063 list at first sign-in, pull in blobs from the user's other servers, publish a drive as a nostr event, encrypted files

## Lineage

Lotus is a fork of [sebdeveloper6952/blossom-server](https://github.com/sebdeveloper6952/blossom-server), which provided the original BUD protocol backend implementation. The original project laid the foundation for the blob storage, access control, and Nostr authentication layers that Lotus builds on.

Lotus diverged to add a decoupled web frontend, the data-directory deployment model, embedded migrations, 0x0.st backend support, and BUD-08 NIP-94 metadata — while tracking upstream bug fixes and protocol improvements where they apply.

## License

See [LICENSE](LICENSE).

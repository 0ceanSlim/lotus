# Lotus handoff

State of the codebase as of 2026-10-06, written for whoever picks this up next, human or AI. Read this before touching anything; it records decisions that are not obvious from the code.

## What lotus is

A [Blossom](https://github.com/hzrd149/blossom) media server (Go, gin, SQLite with blobs stored in the database) with a personal **drive** frontend on top. Most uploads never touch the UI: Nostr clients attach an image to a post, pick a server from the user's kind 10063 list, and the blob lands here with no name, folder or context. The drive is the management layer over those blobs. It is not a social feed and never shows other people's uploads.

Built on the owner's other two projects:

- **grain** (`github.com/0ceanslim/grain` v0.8.0): the Nostr client library and reference web client. Lotus imports `client/core`, `client/session`, `client/connection`, `client/cache`, `client/data` and mounts handlers from `client/api`. It does **not** import the top-level `client` package or `server/handlers`, because those pull in the relay's cgo database. A local checkout usually lives at `C:\code\grain`.
- **mill** (`nostr-mill` 1.8.2): the sign-in modal and signer web component, vendored at `frontends/drive/static/mill/mill.umd.min.js`. Checkout at `C:\code\mill`.

The owner (0ceanslim) wrote grain and mill, so when lotus needs something from them, changing grain or mill is a legitimate option.

## Repository map

```
cmd/lotus/main.go            entry point: data dir, config store, services, grain client, router
embed.go                     go:embed of frontends/drive (package lotus, DriveFS)
internal/api/                gin routes
  routes.go                  pages, legacy API, Blossom endpoints, short-link redirect, /setup, /admin
  v1_routes.go               /api/v1: grain handlers, drive files, short links, admin API mount
  admin.go                   admin config read/update, users list, setup claim
  template_engine.go         fs.FS-based html/template rendering, static mounts, PageData
  auth_middleware.go         Blossom kind-24242 auth (uses grain's signature check)
  auth_controller.go, session_middleware.go, user_stats_controller.go, profile_controller.go
                             LEGACY surface for the 0x0 frontend. Do not change its wire shapes.
  bud0X_controller.go        Blossom handlers
internal/config/             Config type, Store (load, validate, hot-apply hook, atomic write-back), default.yml
internal/core/               domain interfaces (Services, BlobStorage, ShortLinkService, ...)
internal/service/            implementations; services.go wires them and exposes Apply(config)
internal/db/                 hand-written sqlc-style queries + embedded migrations (sql-migrate)
internal/nip98/              NIP-98 HTTP auth verification (+ tests)
internal/grainclient/        grain client init (session manager, relay pool, cleanups)
internal/bud01..06, bloburl, hashing, zeroxzero
frontends/drive/             the drive frontend (embedded; see below)
frontends/0x0/               the ephemeral 0x0-backed deployment's frontend. Frozen. Do not touch.
.dev-data/                   gitignored local data dir (config.yml, db/, web -> junction to frontends/drive)
.claude/launch.json          gitignored dev launcher: go run ./cmd/lotus --data-dir .dev-data
```

## Hard invariants

1. **The 0x0 deployment must keep working unchanged.** It is a separate managed deployment with its own data dir whose `web/` holds `frontends/0x0`. It depends on the legacy endpoints `/api/auth/*`, `/api/user/*`, `/api/profile`, `/list-all`, `/stats`, the page routes `/`, `/my-media`, `/settings`, and static `/scripts`, `/res`. Those stay as they are. `public_listing` defaults to true (nil) precisely so that deployment needs no config edit.
2. **Blob URLs never change.** They are `cdn_url + "/" + sha256 + extension`. Organizing, naming, short links and anything else in the drive are metadata beside the blob.
3. **A write session proves key possession.** grain's login trusts the claimed pubkey; lotus wraps it (`v1Login`) and requires a NIP-98 authorization signed by that pubkey. Read-only sessions can be created unsigned but cannot see or change files. Never add an endpoint that reads or mutates a user's data from a read-only session.
4. **Admin mutations are NIP-98 signed** by the operator's key, on top of the operator's session. The admin shell and admin reads are session-gated only.
5. **No deployment-specific values in code.** Everything a visitor sees comes from `config.yml`. The only fixed strings are the Lotus author credit in the footer and links to the Blossom specs. The owner's own values live in the gitignored `.dev-data/config.yml` and in production.
6. **Uploads from other clients are the norm.** All policy (MIME, size, quota, access rules, future blocklist and scanning) must live on the Blossom API path, never only in the UI.

## Config

`internal/config/config.go` owns it. `config.Load(path)` writes `default.yml` if the file is missing (first run), validates, and returns a `Store`. Handlers read snapshots with `store.Get()`. `store.Update(mutate)` validates, calls the apply hook (`services.Apply`, which reloads access rules, MIME allow list, size limit, quota and the nostr.json URL), writes the file atomically, then publishes. Comments are lost on write-back; the file header says so.

Restart-required keys: `db_path`, `log_level`, `api_addr`, `cdn_url`. Everything else is hot.

Keys added during the rebuild: `public_listing`, `index_relays`, and the `server` block (`name`, `description`, `icon`, `contact`, `terms_url`, `privacy_url`, `cost` free|paid, `membership_url`, `profile_url` template with `{npub}`/`{hex}`, default njump).

`admin_pubkey` empty means unclaimed: every page shows a banner and `/setup` lets the first signed-in user claim it (`store.Claim`), which also adds an UPLOAD rule for them. Access rules: a resource with no rules is denied (changed from the old "invalid state" error).

## Frontend (`frontends/drive`)

Vanilla JS modules, htmx 2 (vendored), Tailwind v4 **browser build** (vendored, compiles at runtime; the `@theme` block in `layout.html` registers token names, `static/css/tokens.css` holds the real values per theme). No build step, no CDN requests. Static assets are served with `Cache-Control: no-cache`.

Themes: `lotus` (default, near-black with hot pink `#ff5cb8`), `dark`, `light`, `grain`, `midnight`. Add a theme in `tokens.css` and `theme.js`. mill's modal is themed by passing `var(--color-*)` references as a theme object (`millTheme()` in `mill-bridge.js`), because mill applies named themes as inline styles that beat any stylesheet.

Scripts, load order fixed in `layout.html` with `defer`:

- `theme.js` (sync, before paint), `mill-bridge.js` (sign-in, NIP-98 login, signer restore across reloads, `window.ensureSigner`, `window.lotusNip98.header`, `window.lotusAfterLogin` hook), `navigation.js` (header state, dropdown, toasts, `window.lotusFmt`, `window.lotusCopy`), `publish.js` (sign + stream-publish with per-relay toast, stamps `["client","lotus"]`), `media-servers.js` (kind 10063 list editor + drive banner), `landing.js`, `drive.js`, `settings.js`, `setup.js`, `admin.js`.
- Views: `index.html` (server page, always at `/`), `drive.html` (`/drive`), `settings.html` (`/drive/settings`), `setup.html`, `setup-claimed.html`, `admin.html`. Each view calls its module's `init()` via a small inline script that tolerates either load order.

Pages are server-rendered Go templates from an `fs.FS`: the data dir's `web/` if present, else the embedded copy. `PageData` carries `Server` (identity), `LoggedIn`, `Pubkey`, `IsAdmin`, `Unclaimed`, `Data` (per-page payload).

## HTTP surface (new)

| Route | Notes |
|---|---|
| `POST /api/v1/auth/login` | grain login wrapped with NIP-98 proof for write mode |
| `GET /api/v1/session`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/amber-callback` | grain |
| `GET /api/v1/cache`, `POST /api/v1/cache/refresh`, `GET /api/v1/user/profile` | grain |
| `GET /api/v1/user/media-servers`, `POST .../build`, `POST /api/v1/events/publish[/stream]` | grain; kind 10063 flow |
| `/api/v1/keys/convert/public/*`, `/api/v1/keys/decode/nip19/*` | grain |
| `GET /api/v1/server/info` | identity, limits, BUDs, claimed, profile template; leading fields match grain's `MediaServerInfo` |
| `GET /api/v1/drive/files` | session user's blobs newest first, totals, short links |
| `POST /api/v1/drive/files/:hash/short`, `DELETE /api/v1/drive/short/:code`, `GET /s/:code` | short links, 302 to canonical |
| `GET/POST /api/v1/admin/config`, `GET /api/v1/admin/users` | admin; POST needs NIP-98 |
| `GET/POST /setup`, `GET /admin` | claim flow, admin shell |

The Blossom endpoints are unchanged: `GET|HEAD /<sha256>`, `PUT /upload`, `HEAD /upload`, `PUT /mirror`, `DELETE /<sha256>`, `GET /list/<pubkey>` (public, on purpose: other clients use it), `GET /stats`, `GET /list-all` (config-gated).

## Database

SQLite via `mattn/go-sqlite3` (cgo). Migrations in `internal/db/migrations`, run at start. Tables: `blobs` (bytes in the row; `hash` is the primary key with one `pubkey`), `blobs_0x0`, `mime_types`, `sessions` (legacy), `short_links`. Queries are hand-written in the sqlc style; there is no sqlc config.

Known data-model limitation to fix next: `blobs.hash` as primary key means two users uploading identical bytes share one row owned by the first uploader; the second never sees it in their list. The planned fix is a per-user `files` table (pubkey, hash, name, folder, added) over a dedup `blobs` table. Until then, delete checks `Blob.Pubkey` (this was a real bug: the descriptor never carried the pubkey, so anyone could delete anything; fixed, with a test in `internal/bud02`).

## Dev loop

```
go run ./cmd/lotus --data-dir .dev-data
```

`.dev-data/web` is a junction to `frontends/drive`, so template, script and CSS edits show on reload. Go and config edits need a restart. The dev config mirrors production with localhost URLs; the owner's key is the admin key there. grain keeps sessions in memory, so **every restart signs everyone out**.

Tests: `go test ./...` covers NIP-98, delete ownership, short links, blob URLs. There is no browser test harness; the drive, settings, setup, claim and admin flows were verified by hand.

## Production

The live instance is `https://blossom.oslim.dev`, data dir `H:\blossom-server` on the owner's Windows machine (config.yml, db with ~2.4 GB, web/ holding the old gallery frontend). Deploy: build, stop lotus, delete or replace `web/` so the embedded drive serves, add `public_listing: false` and a `server` block (see `.dev-data/config.yml` for the owner's values), start. Migrations apply automatically. Remember the first admin save strips comments from config.yml.

## Decisions already made (don't relitigate)

- No public gallery or feed in the drive. The old `frontends/gallery` is deleted.
- `/` is always the server page; `/drive` is the file page; sign-in lands on `/drive`.
- Folders will be server-side metadata (not a kind 30563 drive event): blossom-drive tried the all-in-one event and its author deprecated it. A later "publish drive to nostr" export can be 30563-compatible.
- `GET /list/<pubkey>` stays public; gating it would break clients that show users their own uploads.
- Short links redirect with 302 so revocation and deletion take effect immediately.
- The only media server lotus suggests is itself; grain's curated suggestion endpoint is not mounted.
- Profile links go through the configurable `profile_url` template; njump is only the default.
- The "members" idea for the server page: show only users who publicly list this server in their kind 10063 list, plus an opt-in toggle. Not built yet.

## Next steps, in the intended order

1. **Session persistence.** Add a `Restore`/`Import` method to grain's `SessionManager` (or expose a constructor that takes existing sessions), then persist lotus sessions in SQLite and reload them at startup. Restarts currently log everyone out.
2. **Files table and organization.** Per-user `files` rows; folders as metadata; names; trash with restore; search. Keep `blobs` deduplicated by hash. Update `ListMeta`, `Meta`, delete and the drive listing accordingly; the 0x0 backend keeps working through the same interfaces.
3. **Share pages.** `/f/<code>` style landing pages with preview, name, owner profile (via grain), and the short-link code; folder share pages.
4. **"Used in" context.** Fetch the user's notes from their outbox with grain, scan content and `imeta` tags for this server's URLs/hashes, cache the map, show it per file and warn before deleting a file embedded in posts.
5. **Safety.** BUD-09 `PUT /report` (kind 1984 with `x` tags) into a queue; a `blocked_hashes` table enforced on upload and mirror; a report queue and blob search/delete/block in admin; optional ClamAV over clamd; default MIME deny for executables; per-pubkey upload rate limit.
6. **Nostr extras.** "Pull in my blobs from my other servers" (resolve the user's 10063 list, list each server, BUD-04 mirror here); publish a drive as a 30563-compatible event; client-side encrypted files with NIP-44 key sharing.
7. **Polish.** Precompiled Tailwind CSS for production (the browser build warns it is for development), server-side thumbnails/blurhash emitted as BUD-08 tags, a members section on the server page, the kind 10063 "set up" check on the server page.

## Gotchas

- grain's `server/utils/log` is a no-op until initialized, so grain's own handlers log nothing in lotus. `core.SetLogger` only covers `client/core`.
- `connection.InitializeCoreClient` expects a grain `ServerConfig`; `grainclient.Init` builds one from `core.DefaultConfig()` because a zero-valued one would disable retries and keep-alive.
- grain's `BuildMediaServersHandler` stamps a `client` tag named after grain's config; lotus passes `client_tag: false` and `publish.js` adds `["client","lotus"]` before signing.
- mill signing prompts: with an encrypted key, the first signature after a reload asks for the session password; approving "This session" avoids repeat prompts per event kind.
- The legacy `/settings` route renders the drive's `settings.html` ungated on a drive deployment; it is unlinked and the scripts handle the logged-out state. `/drive/settings` is the gated one.
- `.gitignore` ignores `config.*.yml`, so a committed example config needs a different name (the embedded default is `internal/config/default.yml`).
- Windows: the dev `web` link is an NTFS junction (`mklink /J`); `os.DirFS` follows it fine.

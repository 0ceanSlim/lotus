# Lotus handoff

State of the codebase as of 2026-11-14, written for whoever picks this up next, human or AI. Read this before touching anything; it records decisions that are not obvious from the code.

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
  ratelimit_middleware.go    per-network upload limiter on PUT /upload, PUT /mirror, HEAD /upload
  access_log.go              ginzap access/panic log, or the address-free variant while the limiter is on
  auth_controller.go, session_middleware.go, user_stats_controller.go, profile_controller.go
                             LEGACY surface for the 0x0 frontend. Do not change its wire shapes.
  bud0X_controller.go        Blossom handlers
internal/config/             Config type, Store (load, validate, hot-apply hook, atomic write-back), default.yml
internal/core/               domain interfaces (Services, BlobStorage, ShortLinkService, ...)
internal/service/            implementations; services.go wires them and exposes Apply(config)
internal/ratelimit/          in-memory per-network upload limiter (token buckets, keyed hashes, client IP rule)
internal/db/                 hand-written sqlc-style queries + embedded migrations (sql-migrate)
internal/nip98/              NIP-98 HTTP auth verification (+ tests)
internal/grainclient/        grain client init (session manager, relay pool, cleanups)
internal/bud01..06, bloburl, hashing, zeroxzero
frontends/drive/             the drive frontend (embedded; see below)
frontends/0x0/               the ephemeral 0x0-backed deployment's frontend. Everything new in it is
                             behind rate_limit.anonymous_uploads; with that off it renders as before.
.dev-data/                   gitignored local data dir (config.yml, db/, web -> junction to frontends/drive)
.dev-data-0x0/               gitignored second data dir mirroring the 0x0 deployment (web -> frontends/0x0)
.claude/launch.json          gitignored dev launcher: lotus-dev (8484) and lotus-0x0 (8585)
```

## Hard invariants

1. **The 0x0 deployment must keep working unchanged.** It is a separate managed deployment (data dir `H:\blossom-server-0x0`, public URL `https://0x0.oslim.dev`, 0x0 backend at `localhost:5001`, `log_level: TRACE`) whose `web/` holds `frontends/0x0`. It depends on the legacy endpoints `/api/auth/*`, `/api/user/*`, `/api/profile`, `/list-all`, `/stats`, the page routes `/`, `/my-media`, `/settings`, and static `/scripts`, `/res`. Those stay as they are. `public_listing` defaults to true (nil) precisely so that deployment needs no config edit. Since 2026-11-14 the 0x0 frontend has an opt-in second mode (anonymous uploads and mill sign-in, see below) that the server switches on with `rate_limit.anonymous_uploads`; with it off, `index.html` renders the original markup and inline script from its `{{else}}` branch, so the deployed page is unchanged until the operator flips the config.
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

### `rate_limit` block (added 2026-11-14)

Per-network upload limits, the server half of anonymous uploads. All hot (the apply hook in `main.go` chains `services.Apply` and `limiter.Reload`); the block is `omitempty`, so a deployment that never sets it never sees it written back.

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Mount the limiter. Off means every path below is a no-op and logging is exactly as before. |
| `anonymous_uploads` | `false` | The 0x0 frontend uploads with a browser-made throwaway key and offers mill sign-in. Requires `enabled` (validated). Also exposed as `anonymous_uploads` on `/api/v1/server/info` and as `Limits.AnonymousUploads` in `PageData`. |
| `uploads_per_minute_per_ip` | `10` in default.yml | Token bucket per address: burst and sustained rate. `0` = no limit on this dimension. |
| `bytes_per_window_per_ip` | `524288000` (500 MB) | Byte bucket per address: a burst of this much, refilled evenly over `window`. Must be ≥ `max_upload_size_bytes` when both are set (validated). `0` = no limit. |
| `window` | `"24h"` | Go duration. Also the lifetime of the pubkey→address binding. |
| `max_concurrent_per_ip` | `3` | Uploads in flight per address. `0` = no limit. |
| `exempt_trusted_pubkeys` | `false` | Skip the limiter for pubkeys named in an ALLOW UPLOAD rule or listed in a nostr.json (`ACRService.IsTrusted`). The ALL rule does not count. |
| `trusted_proxies` | none | Addresses/CIDRs, besides loopback (always trusted), whose `CF-Connecting-IP` / `X-Real-IP` header names the real client. |

`enabled: true` with all three limits at zero is rejected, as is `anonymous_uploads` without `enabled`.

### Overall storage cap (added 2026-11-14)

| Key | Default | Meaning |
|---|---|---|
| `max_total_storage_bytes` | `0` (no cap) | Bytes the whole server may hold (the `blobs` or `blobs_0x0` table, whichever backend is on). Must be ≥ `max_upload_size_bytes` when both are set. |
| `when_full` | `"refuse"` | At the cap: `refuse` answers 507 Insufficient Storage with `X-Reason` until space frees up; `evict` deletes blobs ranked by **age × size** (largest product first, age floored at one hour) until the new one fits. An old big file goes before an old small one or a fresh big one; among files from the last hour the biggest goes first. The floor exists because the first live test uploaded three 20 MB files within one second: the newest scored zero and the loop deleted a dozen tiny old blobs, freeing nothing, before reaching it. |

Both are hot. `EvictsWhenFull()` drives the retention label (`best-effort` instead of `permanent` on a non-0x0 server; the 0x0 label stays `ephemeral`), the `retention`/`when_full`/`max_total_storage_bytes` fields of `/api/v1/server/info`, and `Limits.Evicts` in `PageData`, which the 0x0 page turns into a "the retention curve is a ceiling, not a promise" line. Implementation: `internal/service/storage_policy.go` (`storageGovernor`, embedded by both blob services, serialised under a mutex, 50 candidates per pass, short links of evicted blobs removed, one Info log line per eviction with hash, size and age, never a pubkey or address), queries in `internal/db/storage.sql.go`, and `BlobStorage.EnsureSpace`, called by upload and mirror only for a blob that is actually new (a dedupe hit needs no room). The per-pubkey quota is now also enforced on the 0x0 backend (`GetTotalStorage0x0ByPubkey`); it had been a no-op there.

## Frontend (`frontends/drive`)

Vanilla JS modules, htmx 2 (vendored), Tailwind v4 **browser build** (vendored, compiles at runtime; the `@theme` block in `layout.html` registers token names, `static/css/tokens.css` holds the real values per theme). No build step, no CDN requests. Static assets are served with `Cache-Control: no-cache`.

Themes: `lotus` (default, near-black with hot pink `#ff5cb8`), `dark`, `light`, `grain`, `midnight`. Add a theme in `tokens.css` and `theme.js`. mill's modal is themed by passing `var(--color-*)` references as a theme object (`millTheme()` in `mill-bridge.js`), because mill applies named themes as inline styles that beat any stylesheet.

Scripts, load order fixed in `layout.html` with `defer`:

- `theme.js` (sync, before paint), `mill-bridge.js` (sign-in, NIP-98 login, signer restore across reloads, `window.ensureSigner`, `window.lotusNip98.header`, `window.lotusAfterLogin` hook), `navigation.js` (header state, dropdown, toasts, `window.lotusFmt`, `window.lotusCopy`), `publish.js` (sign + stream-publish with per-relay toast, stamps `["client","lotus"]`), `media-servers.js` (kind 10063 list editor + drive banner), `landing.js`, `drive.js`, `settings.js`, `setup.js`, `admin.js`.
- Views: `index.html` (server page, always at `/`), `drive.html` (`/drive`), `settings.html` (`/drive/settings`), `setup.html`, `setup-claimed.html`, `admin.html`. Each view calls its module's `init()` via a small inline script that tolerates either load order.

Pages are server-rendered Go templates from an `fs.FS`: the data dir's `web/` if present, else the embedded copy. `PageData` carries `Server` (identity), `Limits` (max upload size, and when the limiter is on: `AnonymousUploads`, per-network numbers and a human `Window`), `LoggedIn`, `Pubkey`, `IsAdmin`, `Unclaimed`, `Data` (per-page payload). Template funcs: `assetVersion`, `shortKey`, `humanBytes`.

## Frontend (`frontends/0x0`) and anonymous uploads

The 0x0 page is one self-contained Go template (`views/index.html`, black, Courier, no framework). It now has two branches on `.Limits.AnonymousUploads`:

- **Off (the deployed state until the config is flipped):** the original markup and inline script, verbatim. Visitors sign the kind-24242 upload with a NIP-07 extension via `window.nostr`. No session, no mill.
- **On:** the drop zone uploads without a sign-in. `scripts/pages/upload.js` makes a fresh keypair per upload with `scripts/core/throwaway-key.js`, signs the upload authorization with it, `PUT /upload`s with a base64url (BUD-11) token, and shows the link plus the nsec once with "short link", "delete now", "copy key" and "save key as file" buttons. The secret lives only in page memory. A small "sign in" at the top right (and one in the intro line) opens mill (`scripts/vendor/mill.umd.min.js`, same build as the drive's) themed to the page by literal `--mill-*` values; methods are extension, NIP-46, pomegranate (mill's default njump ecosystem, `pomegranate: true`), and under "more options" pasted key, new key and Amber (clipboard return, so no callback route). While signed in, the top bar shows the npub and "sign out", uploads and deletes are signed by the mill signer, the page lists the pubkey's blobs from `GET /list/<pubkey>` with a delete button each, and the signer is restored across reloads with `MILL.restore` from `{method, pubkey}` in `localStorage["0x0.signer"]`. **No server session is created on either path**: nothing on the 0x0 page needs one, every Blossom request carries its own signed authorization, and the legacy `/api/auth/*` surface stays untouched.
- **Short links:** "short link" on the result card calls `POST /api/v1/short/<sha256>` with a NIP-98 authorization (kind 27235, standard base64, signed by the throwaway key or the mill signer), which mints or returns `/s/<code>` under that pubkey; the server side is `signedShortLink` sharing `mintShortLink` with the drive's session-gated endpoint, and each signed request is charged to the network like an upload (`chargeOnce`) so a key cannot fill the table. It is a button rather than automatic so a mill user is not asked for a second signature on every upload. The saved key file records the short link too. Links vanish with the blob (delete or expiry), so an expired 0x0 file's short link 404s like its long one.
- **Ownership and dedupe:** identical bytes dedupe to the first uploader's row, so on a busy 0x0 the person who just dropped a file often is not its owner (any re-upload of the same bytes, by a one-time key or a real one). Two consequences, both handled: `mintShortLink` lets any key shorten any blob when `public_listing` is on (a short link is a bookmark to something already public; on a drive with private blobs it stays owner-only), and the page compares the returned descriptor's `pubkey` with its own key. When they differ it says "this exact file was already on the server under another key…", hides "delete now" (which would fail with "pubkey mismatch") and does not show the one-time nsec (it could do nothing). Found in the owner's first test: the same test file uploaded twice.
- The footer's "need persistent storage?" link now points at `blossom.oslim.dev` with membership at `happytavern.oslim.dev` in both branches: `happytavern.co` no longer exists (2026-11-14), so this is a dead-link fix that intentionally touches the off-mode page too. The `<title>` still says `0x0 // happytavern`, which is the membership brand.

`throwaway-key.js` is a dependency-free BIP-340 signer (BigInt arithmetic, WebCrypto SHA-256), bech32 for nsec/npub, and NIP-01 event signing, written because mill's public API exposes no key generation or private-key signer and vendoring noble would have meant a bundling step. It is only ever used for throwaway keys; real keys are signed by mill. `internal/api/throwaway_key_test.go` runs it under node against the first BIP-340 vectors, grain's nsec/npub decoders, grain's `validation.CheckSignature` (the server's own check) and `nostrAuthMiddleware`; it skips when node is absent.

The legacy pages `/my-media` and `/settings` and the scripts under `scripts/core` and `scripts/pages` other than `upload.js` are left exactly as they were. Note that the 0x0 layout never loads them and never renders the header, so on the 0x0 deployment those pages are unreachable and were dormant before this change too.

### Limiter design (`internal/ratelimit`)

Why: throwaway keys make per-pubkey quota meaningless, so the limits attach to the client network and apply to every upload, signed by a fresh key or a real one, after the signature check and before the handler. Minting keys buys nothing.

- Three token buckets per address: uploads per minute (count), bytes per window (capacity `bytes_per_window_per_ip`, refilled at capacity/window per second, so "500 MB per day" is a 500 MB burst then a steady 500 MB/day), and uploads in flight. Each dimension is independent and `0` disables it.
- **pubkey → address binding:** a pubkey seen for the first time is tied to the address it came from for one window (fixed TTL from first sight, not refreshed). While bound, its uploads from any other address must fit, and are charged to, both that address and the current one. Switching addresses with the same key therefore charges the first one; switching keys at the same address charges that address. The binding is never consulted for anything else.
- **Privacy:** the tables are keyed by `HMAC-SHA256(secret, kind || 0 || value)[:16]` with a 32-byte secret drawn from `crypto/rand` at startup and held only in the process. No address or pubkey is stored in clear, nothing is written to the database, log or disk, a restart forgets everything, and no address↔pubkey association can outlive one window. IPv6 is bucketed by /64. Idle buckets are swept after a window; the table is capped at 250k entries with idle ones evicted first (which only makes the limiter more lenient).
- **Client address:** `Limiter.ClientIP` uses the peer address unless the peer is loopback or in `trusted_proxies`, in which case `CF-Connecting-IP`, then `X-Real-IP`, is believed. `X-Forwarded-For` is deliberately not read (clients can prepend to it). The Cloudflare tunnel on the 0x0 machine connects from loopback, so no config is needed there. gin's own `ClientIP()` (trusts all proxies by default) is not used for limiting.
- **Refusals:** 429 with `Retry-After` (seconds until a token / enough bytes / 5 s for concurrency; the whole window for a file bigger than the budget) and `X-Reason`, plus the usual JSON `{"message"}`; `HEAD /upload` reports the same without charging and adds `X-Upload-Message` for BUD-06 clients. `PUT /mirror` is charged the mirrored blob's size via `ctxUploadBytes` set by the handler. The byte charge is the declared `Content-Length` up front (so a budget check happens before the body is read) and whatever more was actually read afterwards; a chunked body is charged after the fact.
- **Logging:** while `enabled`, the access log is lotus's own variant of ginzap's line without the `ip` field, and the panic recovery logs route and stack without `httputil.DumpRequest` (whose headers would carry the forwarded address). Both are chosen per request, so flipping the flag needs no restart. With the limiter off, ginzap's standard middlewares run exactly as before. The limiter itself logs nothing. Outside lotus, the Cloudflare tunnel/dashboard and the 0x0 backend (`localhost:5001`) keep their own logs; the 0x0 backend sees only lotus's loopback address, Cloudflare sees everything.

### Upload body cap and mirror hardening (2026-11-14)

- `PUT /upload` refuses a declared `Content-Length` over `max_upload_size_bytes` with 413 before reading, and wraps the body in `http.MaxBytesReader` at the limit, so an undeclared (chunked) body is cut off there instead of being buffered whole; 413 carries `X-Reason`. Before this the whole body was read into memory and only then checked. With `max_upload_size_bytes: 0` nothing is capped. Storing errors map in `writeBlobError`: 507 for a full server, 413 for the size limit, 400 for everything else as before.
- `PUT /mirror` was missing the access-rule check and the per-pubkey quota (anyone with any key could store a blob on the members-only drive through it) and fetched any URL with no size cap, internal addresses included. It now runs the same checks as upload (`ResourceUpload`, since a mirror puts bytes on the server under your key; the `MIRROR` rule resource stays unused), refuses a declared size over the limit before reading and reads at most limit+1 bytes, only follows http/https with at most five redirects, and only connects to public addresses: `internal/bud04/safeclient.go` resolves every name and checks every address at dial time (so DNS rebinding cannot steer it) and ignores the environment proxy. Reserved ranges: loopback, RFC 1918, CGNAT 100.64/10, link-local, 0/8, 192.0.0/24, documentation and benchmark ranges, multicast, 240/4, and for IPv6 loopback, unspecified, ULA, link-local, multicast, 2001:db8::/32, NAT64 and 6to4 through their embedded IPv4. The controller's missing `return`s after early aborts are fixed too.

### Decisions on the open questions (2026-11-14)

- **Budget numbers.** Defaults in `default.yml`: 10 uploads/min, 500 MB per 24 h, 3 in flight per address. The 0x0 deployment allows 50 MB per file and nominally 100 MB per key (the 0x0 blob service never enforced that quota: `ValidateStorageQuota` is a no-op there). 500 MB/day/address is ten of the old per-key quotas, enough for a household or office behind one address, and bounds an abuser to ~15 GB/month per address, all of it expiring within 30–90 days. The dev config uses 4/min to make the 429 path easy to reach by hand.
- **Shared addresses (CGNAT, Tor, VPN).** "Signed in" is not a trust signal on a server where anyone can mint a key, so there is no bypass for having a session. The flag that exists is `exempt_trusted_pubkeys`, where trusted means operator-vouched: an explicit ALLOW UPLOAD rule or nostr.json membership. Default **off** (the same rules for everyone); the operator turns it on if members behind shared addresses get throttled by strangers. Members also keep their per-pubkey quota regardless.
- **Save the nsec as a file.** Yes. The clipboard is lost on the next copy, and for a visitor without an extension a file is the only durable place; the key is worth nothing beyond deleting one ephemeral file, so a saved copy carries little risk. The file also records the blob URL and npub.
- **Hashed in-memory vs. a per-connection limiter with no address at all.** Hashed in-memory, as implemented. A limiter with no notion of address cannot carry anything between connections, and an attacker opens a new connection per upload, so it degrades to a global limit that punishes everyone equally. The keyed-hash table is the least state that still attaches cost to a network; its privacy bar is set by the per-process secret, memory-only tables, the one-window binding TTL, /64 bucketing and the address-free access log. The remaining exposure is a live memory dump of the process together with its secret, which recovers hashes but not addresses without a brute-force over the address space, and whatever Cloudflare logs on its side.
- **Per-IP as the storage unit for anonymous keys, and what else there is.** With random keys a per-key quota means nothing, so the per-network byte budget is the storage throttle for anonymous uploads and the overall cap with eviction is the backstop that bounds the total whatever anyone does. Per-network has known soft spots: a shared address (CGNAT, campus, Tor exit) throttles neighbours, and a determined party can rotate addresses (IPv6 is bucketed by /64 for that reason; the `exempt_trusted_pubkeys` flag helps vouched-for members behind shared addresses). The per-key quota stays meaningful for real keys and is now enforced on 0x0 too. Measures that do not depend on identity or address, considered but not built: **proof of work on the authorization event** (NIP-13 style: the client mines a `nonce` tag so the kind-24242 id has N leading zero bits, scaled with the file size, before signing; works with every signer since mining precedes the one signature; the server only counts bits) is the one that scales against key minting and address rotation without tracking anyone, and would be a small addition (`upload_pow_bits` in config, a check in `nostrAuthMiddleware`, a loop in `throwaway-key.js`/mill signers); **payment per megabyte** (lightning or ecash) is the real answer for anonymous storage at scale and fits beside the membership site; **vouching** (a member key signing for an anonymous one) is possible but heavy. Cloudflare Turnstile only protects the page, not the Blossom API, so it was not pursued.
- **Also decided:** `anonymous_uploads` lives inside `rate_limit` and is refused without `enabled`, so the dangerous combination cannot be configured. Loopback is always a trusted proxy (the only sane local setup), `trusted_proxies` only adds. The drive frontend ignores the block entirely. Short links on 0x0 reuse the existing `short_links` table and `/s/<code>` redirect through a NIP-98-authenticated endpoint rather than a session, keeping the "every request carries its own signature" rule; they are opt-in per upload (a button) so mill users are not prompted twice.

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
| `POST /api/v1/short/:hash` | short link with a NIP-98 authorization instead of a session (0x0 page, throwaway keys included); owner-only unless `public_listing` is on; rate-limited like an upload |
| `GET/POST /api/v1/admin/config`, `GET /api/v1/admin/users` | admin; POST needs NIP-98 |
| `GET/POST /setup`, `GET /admin` | claim flow, admin shell |

The Blossom endpoints are unchanged: `GET|HEAD /<sha256>`, `PUT /upload`, `HEAD /upload`, `PUT /mirror`, `DELETE /<sha256>`, `GET /list/<pubkey>` (public, on purpose: other clients use it), `GET /stats`, `GET /list-all` (config-gated). With `rate_limit.enabled`, `PUT /upload`, `PUT /mirror` and `HEAD /upload` can answer 429 with `Retry-After` and `X-Reason` (see the limiter design). Authorization tokens may be standard base64 or base64url, padded or not (BUD-11 asks for unpadded base64url; the 0x0 page sends that).

## Database

SQLite via `mattn/go-sqlite3` (cgo). Migrations in `internal/db/migrations`, run at start. Tables: `blobs` (bytes in the row; `hash` is the primary key with one `pubkey`), `blobs_0x0`, `mime_types`, `sessions` (legacy), `short_links`. Queries are hand-written in the sqlc style; there is no sqlc config.

Known data-model limitation to fix next: `blobs.hash` as primary key means two users uploading identical bytes share one row owned by the first uploader; the second never sees it in their list. The planned fix is a per-user `files` table (pubkey, hash, name, folder, added) over a dedup `blobs` table. Until then, delete checks `Blob.Pubkey` (this was a real bug: the descriptor never carried the pubkey, so anyone could delete anything; fixed, with a test in `internal/bud02`).

## Dev loop

```
go run ./cmd/lotus --data-dir .dev-data
```

`.dev-data/web` is a junction to `frontends/drive`, so template, script and CSS edits show on reload. Go and config edits need a restart. The dev config mirrors production with localhost URLs; the owner's key is the admin key there. grain keeps sessions in memory, so **every restart signs everyone out**.

A second data dir, `.dev-data-0x0` (gitignored, `web` junction to `frontends/0x0`, launcher `lotus-0x0` on 8585), mirrors the 0x0 deployment's config with the 0x0 backend **off** (blobs go to SQLite; there is no local 0x0 instance and the production one at `localhost:5001` must not receive test uploads) and `rate_limit` on with `anonymous_uploads: true` and 4 uploads/min. Set `anonymous_uploads: false` and `enabled: false` there to see the original page.

Tests: `go test ./...` covers NIP-98, delete ownership, short links, blob URLs, config validation and write-back of the `rate_limit` block, the limiter (buckets, byte window, binding TTL, keyed hashes, sweep, reload, client-address trust rule), the middleware (429 headers, body counting, mirror charging, HEAD peek, trusted exemption, hot enable), and the JS throwaway signer under node (skipped without node). There is no browser test harness; the drive, settings, setup, claim and admin flows, and on the 0x0 page the anonymous upload, nsec display, delete, 429, mill sign-in with a pasted key, signed-in upload/list/delete, restore across reload and sign-out, were verified by hand in a browser. A tip for driving the drop zone without a file dialog: build a `File`, put it in a `DataTransfer` and dispatch a `DragEvent('drop')` on `#drop-zone`.

## Production

The live instance is `https://blossom.oslim.dev`, data dir `H:\blossom-server` on the owner's Windows machine (config.yml, db with ~2.4 GB, web/ holding the old gallery frontend). Deploy: build, stop lotus, delete or replace `web/` so the embedded drive serves, add `public_listing: false` and a `server` block (see `.dev-data/config.yml` for the owner's values), start. Migrations apply automatically. Remember the first admin save strips comments from config.yml.

The 0x0 instance is `https://0x0.oslim.dev`, data dir `H:\blossom-server-0x0` (api_addr `0.0.0.0:8004`, `web/` = `frontends/0x0` modulo CRLF, 0x0 backend at `localhost:5001`, behind a Cloudflare tunnel). Rolling out anonymous uploads there: build; copy `frontends/0x0` over `web/` (the new files are `scripts/vendor/mill.umd.min.js`, `scripts/core/throwaway-key.js`, `scripts/pages/upload.js` and the changed `views/index.html`); add the `rate_limit` block from `default.yml` with `enabled: true` and `anonymous_uploads: true` (the per-file limit there is 50 MB, so the 500 MB budget validates); restart. Order matters only in one way: a binary without this change ignores the block, and a binary with it serves the old page until the block is on, so either can go first. Check after: the page says "no account needed", `GET /api/v1/server/info` has `"anonymous_uploads":true`, the access log lines have no `ip` field, and a fifth quick upload from one address gets 429. To fall back, set `anonymous_uploads: false` (hot, via the file and a restart since there is no admin page on that frontend) and the original page is back.

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
5. **Safety.** BUD-09 `PUT /report` (kind 1984 with `x` tags) into a queue; a `blocked_hashes` table enforced on upload and mirror; a report queue and blob search/delete/block in admin; optional ClamAV over clamd; default MIME deny for executables. (The per-network upload limiter exists since 2026-11-14; a per-pubkey rate limit on top of it is still open for the drive, where keys are vetted.)
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
- mill's `.mill-btn.primary` puts fixed white text on `--mill-accent`, so a white accent makes the "Connect" button unreadable; the 0x0 theme uses `#777`.
- Dedup by hash bites the 0x0 "your files" list: identical bytes uploaded earlier by any key (a throwaway one included) stay owned by that first key, so a signed-in re-upload of the same bytes returns the existing descriptor and does not appear under the signed-in pubkey. The files-table rework (next steps, item 2) is the fix.
- `mirrorBlob` once had missing `return`s after its early aborts; fixed with the mirror hardening. The mirror tests point the safe client at an httptest server by overriding the unexported `dialCheck`; keep them in package `bud04`.
- `ratelimit_middleware_test.go` and the limiter tests use a fake clock through the unexported `now` field; keep new limiter tests in package `ratelimit` for that reason.

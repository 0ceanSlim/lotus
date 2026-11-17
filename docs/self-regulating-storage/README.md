# Self-regulating ephemeral storage

Draft protocol, 2026-11-15. Applies to any fixed-size store whose objects expire: a Blossom server, a 0x0-style file drop, or a plain disk partition set aside as an "ephemeral section". Lifetimes in days are configuration, not the protocol; the protocol is how the store picks each object's lifetime so its usage holds steady by itself.

`sim.py` in this folder simulates every controller below against the same traffic. Run `python3 sim.py steps|flood|cycle|quiet`.

## 1. The idea

A store of capacity `C` accepts objects and gives each one an expiry time **at write time**. That expiry is a promise: it is never shortened later. The store picks lifetimes so that, whatever the traffic, its usage settles near a target fill and never needs to break a promise to make room. When even the shortest allowed lifetime can't absorb the traffic, the store refuses writes, sharing what capacity there is fairly between sources.

0x0.st is the special case with one fixed curve, `T = T_min + (T_min − T_max)(s/s_max − 1)^3`. It ignores how full the store is and how fast data arrives, so on a busy server it fills to the cap and then refuses almost everything. In the simulation it refused 85–95% of offered bytes in every busy scenario.

## 2. Model

| Symbol | Meaning |
|---|---|
| `C` | capacity in bytes |
| `ρ*` | target fill, e.g. 0.70 |
| `ρ_h` | hard fill: writes are refused above this, e.g. 0.95 |
| `U(t)` | bytes stored now; `ρ = U/C` |
| `B(t)` | offered inflow, bytes per unit time (including writes that end up refused) |
| `s` | object size, `s_min ≤ s ≤ s_max` |
| `T(s)` | lifetime granted to an object of size `s`, `T_min ≤ T ≤ T_max` |
| `K(τ)` | **committed occupancy**: bytes already stored that will still be alive at `t+τ` |

Two facts carry the whole design.

**Little's law.** In steady state, stored bytes = inflow × byte-weighted mean lifetime:

```
U = B · L,     L = E_bytes[ T(s) ]
```

To hold the store at `ρ*`, the mean lifetime must be `L* = ρ* C / B`. Double the inflow, halve the lifetime.

**The store knows its own future exactly.** Every stored object has a fixed expiry, so `K(τ)` is known exactly for every `τ`, not estimated. It is a non-increasing step function that reaches 0 at the latest expiry. A cache can't do this, because its evictions depend on future requests. Ephemeral storage with fixed promises can.

## 3. Three regimes

Lifetime bounds turn into inflow bounds through Little's law:

| Inflow | What happens |
|---|---|
| `B < ρ* C / T_max` | **Under-used.** Even maximum lifetimes can't fill the store. Every object gets `T_max`. Nothing to regulate. |
| `ρ* C / T_max ≤ B ≤ ρ_h C / T_min` | **Regulated.** Some choice of lifetimes holds the store at target. The controller's job. |
| `B > ρ_h C / T_min` | **Overloaded.** Even minimum lifetimes overflow. Some writes must be refused; fairness decides whose (§6). |

So `T_min` sets how hard a flood the store can absorb without refusing anyone (`ρ_h C / T_min`): with 100 GB and `T_min` = 1 day, about 95 GB/day. `T_max` sets how long data lives when the store is quiet.

## 4. The lifetime function

One global control variable `θ` (a time scale) and a fixed size shape:

```
T(s, θ) = clamp( θ · (s_ref / s)^α,  T_min,  T_max )
```

- `α = 0`: every size gets the same lifetime.
- `α = 1`: every object gets the same **byte-time** `s·T`, the resource actually consumed. A file twice as big lives half as long.
- `α = 0.5` (used in the simulation) is in between. The 0x0 cubic is another valid shape; anything non-increasing in `s` works. The controller only moves `θ`.

Byte-time is the right unit for fairness and accounting throughout. Storage cost is bytes × time, not bytes.

## 5. Choosing θ: look-ahead control

Every control period (minutes to a few hours), solve for the **largest θ whose projected occupancy stays under target at every horizon**:

```
for every τ in a horizon grid (1h … T_max):
    K(τ) + B̂ · Σ_b w_b · min(τ, T(s_b, θ))  ≤  ρ* C
```

- `K(τ)`: committed occupancy, read straight from the expiry schedule. Keep a calendar of bytes expiring per hour; `K(τ)` is a prefix sum.
- `B̂`: smoothed offered inflow (EWMA, half-life about a day).
- `w_b`: smoothed byte share of each size bucket (log₂ buckets are enough).
- The second term is how many bytes the new stream will hold `τ` from now if it gets lifetimes `T(s, θ)`.

The left side is increasing in `θ`, so a binary search on `log θ` finds the answer in about 18 steps. If even `θ_min` (every object at `T_min`) violates the bound, use `θ_min` and let admission (§6) refuse. If `θ_max` satisfies it, every object gets `T_max`.

Why this and not a PID loop on fill: fill reacts to a lifetime decision only as old objects expire, which can take months. A feedback loop on current fill keeps pushing after it should stop, overshoots, and then can't undo the lifetimes it already promised. Look-ahead sees the commitments directly and doesn't wind up. In the simulation, PID plus Little's law (`ffb`) refused 75% of bytes in the step scenario, while look-ahead refused 0.4%.

**Simple variant (feedforward only):** `θ = ρ* C / (B̂ · E_bytes[(s_ref/s)^α])`, which is Little's law solved for `θ`. One line, no schedule needed. It tracks steady traffic well (best in the `cycle` scenario) but overshoots on sudden jumps, because it ignores what's already committed. Fine for a first implementation; look-ahead is the full version.

**Stability rules:**
- Recompute `θ` on a timer, never per request.
- Apply it only to new writes.
- Bound how fast `θ` may rise, e.g. at most ×2 per day. Falling should be fast, rising slow.

## 6. Admission and fairness

Lifetime control handles the regulated regime. Above `ρ_h C / T_min` (or at the hard cap for any reason), writes must be refused, and the question is whose.

**Write rule:** accept a write of size `s` iff

1. `s ≤ s_max_eff`, and
2. `U + s ≤ ρ_h C`, and
3. the source has byte-time budget left: `s · T(s, θ) ≤ budget(source)`.

**Sustainable byte-time per source** (max-min fair share): the store can sustain `ρ* C` bytes held continuously, i.e. `ρ* C` byte-time per unit time. With `N` sources active in the last window, each gets a token bucket refilled at

```
r = ρ* C / N_active     (byte-time per unit time)
```

with burst capacity of, say, one window's worth. A single heavy source can't push out everyone else; when few sources are active, each may use more. Rates and concurrency (requests per minute, writes in flight) stay fixed limits, since they protect CPU and bandwidth, not space.

**Secondary lever:** the size cap shrinks under pressure, `s_max_eff = s_floor + (s_max − s_floor) · p(ρ)` with `p = 1` below target and falling to 0 at `ρ_h`. Small objects keep flowing while the store is tight.

**What a "source" is:** whatever is hard to mint. Keys are free, so a key alone is not a source. Use a network address (IPv6 by /64), with the key bound to the first address it was seen from for one window. Held only in memory as keyed hashes under a per-process secret, never logged or persisted, and forgotten on restart. That is enough to rate-limit and keeps the store from becoming a record of who uploaded what.

## 7. Promises (the invariants)

1. An accepted object lives **at least** until its expiry. Nothing is ever evicted early for space. Running out of space means refusing writes, never deleting promised data.
2. The expiry is told to the writer **before** the write (quote) and **with** it (receipt).
3. The owner can delete any time; delete removes the bytes.
4. Writing an object that already exists (same hash) sets its expiry to `max(current, new)` and charges the new writer. Popular objects keep themselves alive without any read tracking.
5. Nothing about sources is stored durably.

Optional cache mode: renew an object's expiry on read (TTL-reset, as in TTL caches). This gives up invariant 1's predictability for readers' benefit. A separate mode, off by default.

## 8. Wire format (HTTP; maps directly onto Blossom)

**State**, e.g. `GET /.well-known/ephemeral` or inside Blossom server info:

```json
{
  "capacity": 107374182400,
  "used": 61203283968,
  "target_fill": 0.70,
  "hard_fill": 0.95,
  "ttl_min": 86400,
  "ttl_max": 31536000,
  "shape": { "kind": "power", "s_ref": 1048576, "alpha": 0.5 },
  "theta": 2592000,
  "size_max_now": 52428800,
  "updated_at": 1763164800
}
```

From `shape` and `theta`, a client computes any object's lifetime locally.

**Quote**, e.g. `HEAD /upload` with `X-Content-Length` (Blossom BUD-06). Response:

```
X-Expires-At: 1767052800
X-Retention: 3888000
```

or a refusal (below).

**Receipt.** The write response includes `"expires_at": 1767052800`.

**Refusals:**
- `413`: too large for now, with `X-Reason`.
- `429`: the source's budget or rate is used up, with `Retry-After`.
- `507`: the store is at the hard cap, with `Retry-After`. The store knows exactly when bytes free up: the first step of `K(τ)`.

## 9. As a disk partition

The protocol needs only three things: a fixed byte budget, an expiry per object, and a single write path that can quote and refuse. On a plain filesystem:

- **Budget:** a dedicated partition, or a directory with a filesystem quota, of size `C`.
- **Expiry:** an xattr (`user.expires_at`) or a sidecar index; a reaper deletes due files every few minutes.
- **Write path:** a small daemon (HTTP, or a FUSE layer) that runs the admission rule, sets the expiry, and keeps the per-hour expiry calendar for `K(τ)`.
- **Recovery:** rebuild the calendar at startup by scanning the xattrs.

The same algorithm serves a Blossom server, a file drop, or a system scratch area.

## 10. Simulation results

100 GB store, `ρ*` 0.70, `ρ_h` 0.95, `T_min` 1 day, `T_max` 365 days, `α` 0.5, log-normal sizes (median 1.5 MB, cap 50 MB), 300 days. "Refused" is the share of offered bytes. Controllers: `fixed` = 0x0 curve (30–365 d); `ff` = Little's-law feedforward; `ffb` = feedforward plus PI on fill; `look` = look-ahead (§5).

| Scenario | Traffic | fixed | ff | ffb | look |
|---|---|---|---|---|---|
| steps | 2 → 10 GB/day, a 3-day 40 GB/day flood, then 0.5 | 85.2% refused | 6.8% | 74.9% | **0.4%** |
| flood | 3 GB/day with 30 days at 80 GB/day | 94.5% | 27.9% | 73.4% | **15.5%** |
| cycle | ~6 GB/day with daily and weekly swings | 90.0% | **0.0%** | 5.1% | **0.0%** |
| quiet | 0.3 GB/day | 0% (under-used) | 0% | 0% | 0% |

Readings:

- **The fixed curve doesn't regulate.** It fills to the cap and refuses almost everything.
- **Look-ahead is the only controller that stays safe through sudden changes.**
- **Flood:** 80 GB/day is close to the overload line (`ρ_h C / T_min` ≈ 95 GB/day). The remaining refusals there are the physical limit, plus data committed before the flood. That is §6's job, not the lifetime controller's.
- **Cycle:** look-ahead sits a little under target (about 0.62), because it plans for the worst horizon. A slow integral trim on `ρ*` would remove that bias if wanted.
- **Quiet:** fill rises slowly, because everything gets `T_max` and steady state takes `T_max` to reach. Correct behavior.

## 11. Related work

- Che's approximation and TTL-cache analysis: an LRU cache behaves like a TTL cache with one "characteristic time". Fofack, Nain, Neglia, Towsley, [Analysis of TTL-based cache networks](https://eudl.eu/pdf/10.4108/valuetools.2012.250250) (2012).
- Adaptive TTL by stochastic approximation, including a variant (f-TTL) that targets an expected cache size. Basu et al., [Adaptive TTL-Based Caching for Content Delivery](https://arxiv.org/abs/1704.04448). Close in spirit, but caches renew on hits and don't hold promises; here lifetimes are commitments, which is what makes look-ahead possible.
- Size-aware admission (small objects admitted with higher probability, tuned by a model). Berger, Sitaraman, Harchol-Balter, [AdaptSize](https://www.usenix.org/conference/nsdi17/technical-sessions/presentation/berger) (NSDI '17). The inspiration for the shrinking size cap.
- Pricing storage as byte × time with a declining cost curve: Arweave's [storage endowment](https://permaweb-journal.ar.io/reference/storage-endowment.html). The opposite end (permanent storage paid up front), same unit.
- 0x0.st's retention curve (the fixed special case).

## 12. Notes for lotus

- The 0x0 backend's own expiry (30–365 days) and lotus's (30–90) disagree, and lotus's deletes don't reach the backend. Lotus sends HTTP `DELETE`, which the backend doesn't route, and never stores the `X-Token` a backend delete needs. Fix that before promising anything about deletion or expiry.
- With this protocol, lotus should own expiry: pass `expires=` to the 0x0 backend on upload (or drop the backend and use §9 directly).
- The per-address limiter from PR #2 is already the "source" layer of §6. What's missing is turning its byte budget into a byte-time fair share.

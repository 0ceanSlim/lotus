# EBB: Elastic Byte Budget

### Self-regulating ephemeral storage. Fixed-capacity storage that holds its own usage steady by choosing, and keeping, every object's lifetime

**OceanSlim**
November 2026 · Draft 1

---

## Abstract

Free, open storage services such as anonymous file drops and public Blossom media servers face a basic tension. Limits loose enough to be useful when the service is quiet are too loose when it is busy, and the usual safety valve, a hard cap that refuses or evicts once the disk is full, punishes everyone at once and without warning. This paper describes EBB, the Elastic Byte Budget, a protocol for a store of fixed capacity in which every object receives an expiry time when it is written, that expiry is a promise never shortened later, and the store chooses those expiries so that its usage settles near a target level on its own.

The design rests on two observations. First, by Little's law, the bytes a store holds equal its inflow multiplied by the mean lifetime it grants, so lifetime is the natural control variable. Second, because expiries are fixed at write time, an ephemeral store knows its own future occupancy exactly. This allows a *look-ahead* controller that plans against data already committed, instead of reacting to current fill as a feedback loop would. In simulation against step changes, floods and periodic traffic, the look-ahead controller refused 0.4% of offered bytes where a fixed retention curve of the 0x0.st kind refused 85%, and a conventional feedback controller 75%. When traffic exceeds what even minimum lifetimes can absorb, a fair-share admission rule measured in byte-time decides whose writes are refused, using only short-lived, in-memory knowledge of sources. The same protocol applies to a networked service or to a plain disk partition set aside as an ephemeral section.

---

## 1. Introduction

Most storage is sold as space: a quota of bytes, held indefinitely. But what storage actually consumes is **space over time**. A gigabyte kept for a year costs 365 times as much as a gigabyte kept for a day. Services that hold data temporarily, such as paste sites, file drops, media servers for social protocols, and scratch areas, already know this informally. They expire old data. Few of them treat expiry as something to control.

The best-known example is 0x0.st, a no-account file host whose retention depends only on file size:

```
retention = T_min + (T_min − T_max) · (s / s_max − 1)^3
```

Small files live up to a year, large ones about a month. The curve is simple and honest, but it is blind: it grants the same lifetimes whether the server is empty or nearly full, and whether ten files a day arrive or ten thousand. A busy server running it fills to capacity and then has nothing left but refusal.

This paper asks what such a store should do instead, given:

1. a fixed capacity it cannot exceed,
2. traffic it does not control and cannot predict, and
3. a commitment to tell each writer, truthfully and in advance, how long their data will live.

The answer is a small control system with one variable, a few invariants, and a fairness rule. It is simple enough to implement in a few hundred lines. We call it **EBB**, the **Elastic Byte Budget**: lifetimes stretch and shrink, the resource being budgeted is bytes over time, and, as in the tide it is named after, what flows in is matched by what ebbs out.

## 2. Background

**Time-to-live caches.** Much of the theory of cache behavior is phrased in terms of TTLs. Che's approximation models an LRU cache as a TTL cache with a single "characteristic time", and Fofack, Nain, Neglia and Towsley showed that TTL caches capture LRU, FIFO and random replacement as special cases [1]. Basu et al. went further and tuned TTLs adaptively by stochastic approximation to hit a target hit rate, and in one variant a target expected cache size [2]. Caches differ from the store described here in one decisive way: a cache renews or evicts entries according to future requests, so its future contents are uncertain. An ephemeral store that keeps its promises has no such uncertainty.

**Size-aware admission.** AdaptSize showed that for a memory cache of widely varying object sizes, admitting small objects with higher probability than large ones, with the admission parameter tuned continuously, substantially improves performance [3]. The same intuition appears here as a size-dependent lifetime and a size cap that tightens under pressure.

**Storage priced over time.** Arweave prices permanent storage as a one-time payment that funds an endowment, computed by summing the expected declining cost of storing bytes over centuries [4]. It sits at the opposite end of the retention spectrum from this work, but the unit is the same: bytes multiplied by time.

**Blossom and Nostr.** Blossom is a protocol for storing blobs on HTTP servers, addressed by SHA-256 and authorized by signed Nostr events [5]. Users list their preferred servers publicly and clients upload to them. Free Blossom servers are exactly the services this protocol is meant for: open to anyone holding a key, and keys cost nothing.

## 3. Model

A store has capacity `C` bytes. Objects arrive with sizes `s` between `s_min` and `s_max`. Each accepted object is granted a lifetime `T` between `T_min` and `T_max` and is deleted when it expires.

| Symbol | Meaning |
|---|---|
| `C` | capacity in bytes |
| `ρ*` | target fill fraction, e.g. 0.70 |
| `ρ_h` | hard fill fraction, above which writes are refused, e.g. 0.95 |
| `U(t)`, `ρ(t) = U/C` | bytes stored, and fill |
| `B(t)` | offered inflow in bytes per unit time, including writes later refused |
| `T(s)` | lifetime granted to an object of size `s` |
| `L` | byte-weighted mean lifetime, `E_bytes[T(s)]` |
| `K(τ)` | committed occupancy: bytes already stored that will still be present at time `t + τ` |

### 3.1 Little's law for storage

In steady state, the bytes a store holds equal the rate at which bytes arrive multiplied by how long, on average, each byte stays:

```
U = B · L
```

Holding the store at target therefore requires a mean lifetime of

```
L* = ρ* · C / B
```

Lifetime is the lever. When inflow doubles, lifetimes must halve; when it falls, they may grow.

### 3.2 The store knows its future

Every stored object has an expiry fixed at write time. The committed occupancy `K(τ)`, meaning how much of what is stored now will still be stored at any future moment, is therefore known exactly. It is a non-increasing step function that starts at `U(t)` and falls to zero at the latest expiry. In practice it is a calendar of bytes expiring per hour, and `K(τ)` is a running sum over it.

No cache has this property, and it is what makes the controller in §5 possible.

### 3.3 Three regimes

Since the store can never grant less than `T_min` or more than `T_max`, Little's law turns the lifetime bounds into inflow bounds:

| Offered inflow | Regime | Behavior |
|---|---|---|
| `B < ρ* C / T_max` | **Under-used** | Even maximum lifetimes cannot fill the store. Everyone receives `T_max`. |
| `ρ* C / T_max ≤ B ≤ ρ_h C / T_min` | **Regulated** | Some lifetime choice holds the store at target. This is the controller's job. |
| `B > ρ_h C / T_min` | **Overloaded** | Even minimum lifetimes overflow. Some writes must be refused, and fairness decides whose. |

These bounds give an operator a direct reading of what their parameters mean. With 100 GB of capacity and a one-day minimum lifetime, the store absorbs about 95 GB/day before anyone is refused. With a one-year maximum, it reaches its target only once inflow exceeds about 200 MB/day; below that it is simply generous.

## 4. Lifetime function

Lifetimes are shaped by size and scaled by a single global variable `θ`, a time scale:

```
T(s, θ) = clamp( θ · (s_ref / s)^α ,  T_min ,  T_max )
```

The exponent `α` fixes how size trades against time:

- `α = 0`: every object receives the same lifetime regardless of size.
- `α = 1`: every object receives the same byte-time `s · T`. An object twice as large lives half as long, so each write consumes an equal share of the resource.
- Values in between soften the penalty on large objects. The 0x0.st cubic is another admissible shape; any shape non-increasing in `s` will do.

The shape is policy and does not change at runtime. Only `θ` moves.

## 5. Look-ahead control

At each control period (minutes to a few hours), the store chooses the **largest `θ` for which projected occupancy stays at or below target at every future horizon**:

```
for every τ in a horizon grid from one hour to T_max:

    K(τ)  +  B̂ · Σ_b w_b · min( τ , T(s_b, θ) )   ≤   ρ* · C
```

- `K(τ)` is the committed occupancy, read from the expiry calendar.
- `B̂` is a smoothed estimate of offered inflow (an exponentially weighted average with a half-life of about a day).
- `w_b` is the smoothed share of incoming bytes falling in size bucket `b` (logarithmic buckets suffice).
- The sum is the volume the incoming stream will occupy `τ` from now if new objects receive lifetimes `T(s, θ)`.

The left-hand side increases with `θ`, so a binary search on `log θ` converges in under twenty steps. Two edge cases:

- If even the smallest `θ` (every object at `T_min`) violates the bound, the store uses it and leaves the excess to admission control (§6).
- If the largest `θ` (every object at `T_max`) satisfies it, the store is under-used and grants `T_max` to all.

### 5.1 Why not feedback

The obvious alternative is a feedback loop that lengthens lifetimes when the store is emptier than target and shortens them when it is fuller. It performs badly here, for a structural reason. A lifetime decision affects fill only as the affected objects expire, which may be months later. By the time fill reflects a change in traffic, the store has already promised long lifetimes to a great deal of data that it cannot take back. The loop overcorrects, and the store spends long periods pinned at its cap. In simulation, adding proportional-integral feedback to an otherwise reasonable controller *raised* refusals from 6.8% to 75% (§8).

Look-ahead control does not react to fill. It plans against commitments it can see exactly, and only one quantity is estimated: the incoming traffic.

### 5.2 A simpler first implementation

Solving Little's law directly for `θ` gives a one-line controller:

```
θ = ρ* · C / ( B̂ · E_bytes[ (s_ref / s)^α ] )
```

It requires no expiry calendar, tracks steady and periodic traffic well, and is a reasonable starting point. Its weakness is sudden change: it ignores data already committed, so it overshoots when traffic jumps.

### 5.3 Stability

- Recompute `θ` on a timer, never per request.
- Apply a new `θ` only to subsequent writes.
- Let `θ` fall quickly but rise slowly (for example, at most doubling per day), so a brief lull does not hand out long lifetimes just before traffic returns.

## 6. Admission and fairness

The lifetime controller handles the regulated regime. When offered traffic exceeds what minimum lifetimes can absorb, or the store reaches its hard cap for any reason, writes must be refused. The question is whose.

### 6.1 Write rule

A write of size `s` is accepted if and only if:

1. `s ≤ s_max_eff`, the current size cap;
2. `U + s ≤ ρ_h · C`; and
3. the writer's source has byte-time budget for `s · T(s, θ)`.

### 6.2 Byte-time fair share

At target, the store sustains `ρ* C` bytes continuously, that is, `ρ* C` units of byte-time per unit time. Divided max-min fairly among the `N` sources active in a recent window, each source's budget refills at

```
r = ρ* · C / N_active        (byte-time per unit time)
```

with burst capacity of roughly one window's refill. A single heavy source cannot crowd out the rest. When few sources are active, each may use more. Request rate and concurrency limits remain fixed, since they protect processing and bandwidth rather than space.

### 6.3 The size cap under pressure

As a secondary lever, the size cap contracts as the store approaches its hard limit:

```
s_max_eff = s_floor + (s_max − s_floor) · p(ρ)

p(ρ) = 1                                     for ρ ≤ ρ*
p(ρ) = ((ρ_h − ρ) / (ρ_h − ρ*))^k            for ρ* < ρ < ρ_h
p(ρ) = 0                                     for ρ ≥ ρ_h
```

Small objects keep flowing when the store is tight, and large objects wait.

### 6.4 What counts as a source

Fairness among sources is only as good as the definition of a source, and it must be something costly to multiply. Cryptographic keys are free, so a key alone is not a source. A network address is costlier: IPv4 by address, IPv6 by /64 prefix, since a single host typically controls a whole /64. Each key is bound to the address it was first seen from for one budget window, so rotating keys from one address does not reset the budget, and moving one key across addresses charges both.

All of this is held **in memory only**, as keyed hashes under a secret generated at process start, never logged and never written to disk. A restart forgets everything. The store learns enough to divide capacity fairly over the next day and nothing it could later be compelled to disclose.

## 7. Invariants

The protocol makes five promises.

1. **No early eviction.** An accepted object remains available at least until its expiry. A store short of space refuses new writes; it never deletes promised data.
2. **Quoted lifetimes.** The writer learns an object's expiry before writing (a quote) and with the write (a receipt).
3. **Owner deletion.** The owner may delete an object at any time, and deletion removes the bytes.
4. **Renewal by rewrite.** Writing an object that already exists, identified by content hash, sets its expiry to the later of the existing and new expiries and charges the new writer. Content people keep sharing keeps itself alive, with no tracking of reads.
5. **No durable record of sources.** Nothing that identifies a writer's network location is persisted.

An optional cache mode, renewing expiry on read as TTL caches do, trades invariant 1's predictability for availability of popular content. It is a separate mode and off by default.

## 8. Evaluation

A discrete-time simulation (hourly steps, 300 days) compared four policies on the same traffic. Parameters:

- **Store:** 100 GB capacity, `ρ*` 0.70, `ρ_h` 0.95.
- **Lifetimes:** `T_min` 1 day, `T_max` 365 days, `α` 0.5.
- **Objects:** log-normal sizes with a 1.5 MB median, capped at 50 MB.

The policies:

- **fixed:** the 0x0.st curve, 30–365 days.
- **ff:** Little's-law feedforward (§5.2).
- **ffb:** feedforward with proportional-integral feedback on fill.
- **look:** look-ahead control (§5).

The table reports the share of offered bytes refused.

| Scenario | Traffic | fixed | ff | ffb | look |
|---|---|---|---|---|---|
| Steps | 2 → 10 GB/day, a 3-day flood at 40 GB/day, then 0.5 GB/day | 85.2% | 6.8% | 74.9% | **0.4%** |
| Flood | 3 GB/day, with 30 days at 80 GB/day | 94.5% | 27.9% | 73.4% | **15.5%** |
| Cycle | about 6 GB/day with daily and weekly swings | 90.0% | **0.0%** | 5.1% | **0.0%** |
| Quiet | 0.3 GB/day | 0% | 0% | 0% | 0% |

What the results show:

- **A fixed curve does not regulate.** Under any sustained load it fills to the cap and then refuses nearly everything.
- **Look-ahead control is the only policy that remains safe through abrupt change.** In the steps scenario it absorbed a five-fold traffic increase and a flood with almost no refusals.
- **The flood scenario sits close to the overload line.** 80 GB/day is near the 95 GB/day the store can absorb at minimum lifetimes. Most remaining refusals are unavoidable, and the rest come from data committed before the flood began. Distributing those refusals fairly is the role of §6, not of the lifetime controller.
- **On periodic traffic look-ahead runs slightly below target** (around 0.62) because it plans for the worst point on the horizon. A slow integral trim on the target removes the bias if desired.
- **In the quiet scenario every object receives `T_max`.** Fill rises slowly, as it must, since steady state cannot be reached faster than the longest lifetime.

The simulation is self-contained and reproducible (`sim.py`, Python standard library only).

## 9. Implications

**Free storage that stays free.** The usual fate of an open upload service is to fill up, start refusing, and eventually require accounts or payment. A store that regulates itself degrades gradually instead: under load, lifetimes shorten for everyone, and refusals begin only when even the minimum cannot be met. Those refusals fall on the heaviest sources first. An operator can run a public service on a fixed disk without watching it.

**Honesty as a protocol property.** Because lifetimes are fixed at write time and quoted in advance, a user always knows what they are getting. "This file will last 41 days" is a commitment the store is structured to keep, not an estimate it may later revise. Most free hosts promise nothing, or promise "permanent" and quietly delete.

**Verifiable receipts.** The same Nostr signing used to authorize uploads lets a store sign its receipt: object hash, size, expiry, store identity. A user who finds a file gone before its signed expiry has portable proof of a broken promise. A client or reputation system can aggregate such proofs across many stores, letting promise-keeping become a measurable property of a server.

**Abuse resistance without surveillance.** The usual answer to abuse of anonymous services is identification: accounts, emails, phone numbers, persistent IP logs. This design bounds abuse by fair-share accounting over sources held only in memory, as hashes, for a single window. It does not stop a determined attacker with many network addresses, and nothing short of identification does. But it bounds each address's share, makes flooding expensive, and leaves the operator with no durable record of who uploaded what.

**Bounded liability and exposure.** Every object has a known end. Unwanted or unlawful material ages out without intervention, and the store's total exposure at any time is bounded by its capacity and its maximum lifetime. Combined with no persistent source records, there is little for an operator to retain, leak or be compelled to produce.

**A market in lifetimes.** Byte-time is a well-defined, meterable unit. The free tier is the fair share. A paid tier is simply a larger share or a higher `θ` for paying keys. Stores can publish their current `θ` and size shape, and clients can choose among them by the lifetime they would receive, much as Blossom clients already choose among a user's listed servers. Many independent small stores, each regulating itself, form a federated pool with no coordinator.

**Beyond the network.** The protocol needs only a fixed byte budget, an expiry per object, and a single write path able to quote and refuse. On an ordinary machine that is a partition or quota-limited directory, an extended attribute holding each file's expiry, a periodic reaper, and a small daemon in front of writes. Build caches, download staging, temporary uploads and log spools all fit the description. Each would stop being a disk that fills at the worst moment and become one that holds a chosen level indefinitely.

## 10. Limitations and open questions

- **Address-based fairness is imperfect.** Users behind shared addresses (carrier-grade NAT, Tor, institutional networks) share one budget, while attackers with many addresses get many. Exempting trusted keys or accepting proof-of-work tokens are possible refinements.
- **Estimation error.** Look-ahead control estimates only incoming traffic, but it still estimates. Traffic that changes faster than the smoothing window is met late, and admission control absorbs the difference.
- **Long maximum lifetimes slow convergence.** A store with a one-year maximum takes up to a year to settle after traffic falls, because it cannot reclaim promised time. Operators who care about responsiveness should choose a shorter `T_max`.
- **The choice of target.** `ρ*` trades utilization against headroom for bursts. Whether it is better set by hand or derived from observed burstiness is open.
- **Content hashing and renewal.** Renewal by rewrite assumes content addressing. Stores that address objects by name need another way to identify repeats.
- **Standardization.** The wire format below is a proposal. Whether it belongs as a Blossom extension, a standalone HTTP convention, or both is open.

## 11. Wire format (proposed)

The protocol maps onto HTTP and, specifically, onto Blossom with small additions.

**Store state** (a well-known document, or a field of an existing server-information document):

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

**Quote.** A pre-flight request with the object's size (Blossom's `HEAD /upload`) returns:

```
X-Expires-At: 1767052800
X-Retention: 3888000
```

**Receipt.** The write response carries `"expires_at"`, optionally with a signature by the store's key.

**Refusals:**

| Status | Meaning | Headers |
|---|---|---|
| `413` | Object exceeds the current size cap | `X-Reason` |
| `429` | The source's budget or rate is exhausted | `Retry-After`, `X-Reason` |
| `507` | The store is at its hard limit | `Retry-After`, derived exactly from the next expiry in the calendar |

## 12. Conclusion

An ephemeral store with honest, fixed expiries knows exactly how much of what it holds today it will still hold at any future moment. Combined with Little's law, that knowledge turns a hard capacity limit into a regulated system. Lifetimes stretch when the store is quiet and shrink when it is busy, usage settles near a chosen level, no promise is ever broken, and when demand truly exceeds supply, the shortfall is shared fairly by a mechanism that remembers no one. The pieces are simple: one control variable, an expiry calendar, a binary search, and a token bucket. They are enough to let a single disk, or a federation of small servers, offer free storage that stays free.

---

## References

1. N. C. Fofack, P. Nain, G. Neglia, D. Towsley. *Analysis of TTL-based Cache Networks.* VALUETOOLS 2012. https://eudl.eu/pdf/10.4108/valuetools.2012.250250
2. S. Basu, A. Sundarrajan, J. Ghaderi, S. Shakkottai, R. Sitaraman. *Adaptive TTL-Based Caching for Content Delivery.* IEEE/ACM Transactions on Networking, 2018. https://arxiv.org/abs/1704.04448
3. D. S. Berger, R. K. Sitaraman, M. Harchol-Balter. *AdaptSize: Orchestrating the Hot Object Memory Cache in a Content Delivery Network.* NSDI 2017. https://www.usenix.org/conference/nsdi17/technical-sessions/presentation/berger
4. Arweave. *Storage Endowment.* https://permaweb-journal.ar.io/reference/storage-endowment.html
5. hzrd149 et al. *Blossom: Blobs Stored Simply on Mediaservers.* https://github.com/hzrd149/blossom
6. 0x0.st, *The Null Pointer*, retention policy. https://0x0.st

#!/usr/bin/env python3
"""Simulate a fixed-capacity ephemeral store under different lifetime controllers.

Plain stdlib. Time unit is one hour. Run:  python3 sim.py [scenario]
Scenarios: steps (default), cycle, flood, quiet

Each upload gets a lifetime fixed at upload time (never shortened later).
The only overflow mechanism is refusal at the hard cap, so promises hold.
"""
import math
import random
import sys

HOUR = 1
DAY = 24
MB = 1 << 20
GB = 1 << 30

# ---- store ---------------------------------------------------------------
C = 100 * GB           # capacity
RHO_T = 0.70           # target fill
RHO_H = 0.95           # hard fill: refuse above this
T_MIN = 1 * DAY        # lifetime floor (a promise)
T_MAX = 365 * DAY      # lifetime ceiling
S_MAX = 50 * MB        # largest file accepted
S_REF = 1 * MB         # size the time scale theta refers to
S_MIN = 1024           # smallest size considered
ALPHA = 0.5            # 0 = same lifetime for every size, 1 = same byte-hours

HORIZON = 300 * DAY
EWMA_HL = 24           # hours, half-life for inflow / size-mix estimates
KP, KI = 2.0, 0.02     # feedback gains on fill error (per hour for KI)
I_LIM = 3.0            # anti-windup on the integral term (log units)


def shape(s):
    """Size-dependent part of the lifetime: (s_ref / s)^alpha."""
    return (S_REF / s) ** ALPHA


# theta range: at THETA_LO every file gets T_MIN, at THETA_HI every file T_MAX
THETA_LO = T_MIN / shape(S_MIN)
THETA_HI = T_MAX / shape(S_MAX)

# look-ahead controller: size histogram (log2 bins) and horizon grid
BINS = [2 ** k for k in range(10, 27)]                 # 1 KB .. 64 MB
TAU_GRID = [1, 3, 6, 12, 24, 48, 96, 168, 336, 720, 1440, 2880, 4380, 8760]
LOOK_EVERY = 3                                          # hours between solves


def bin_of(s):
    return min(len(BINS) - 1, max(0, int(math.log2(max(s, 1))) - 10))


def new_stream(tau, theta, b_hat, w):
    """Bytes the incoming stream will hold tau hours from now if every new
    file gets lifetime(s, theta): B * sum_b w_b * min(tau, T_b)."""
    return b_hat * sum(wb * min(tau, lifetime(BINS[i] * 1.41, theta)) for i, wb in enumerate(w) if wb)


def solve_theta(committed, b_hat, w):
    """Largest theta whose projected occupancy stays under target at every
    horizon: committed(tau) + new_stream(tau) <= RHO_T * C."""
    cap = RHO_T * C
    def ok(th):
        return all(committed[k] + new_stream(tau, th, b_hat, w) <= cap for k, tau in enumerate(TAU_GRID))
    if not ok(THETA_LO):
        return THETA_LO
    if ok(THETA_HI):
        return THETA_HI
    lo, hi = math.log(THETA_LO), math.log(THETA_HI)
    for _ in range(18):
        mid = (lo + hi) / 2
        if ok(math.exp(mid)):
            lo = mid
        else:
            hi = mid
    return math.exp(lo)


def lifetime(s, theta):
    return min(T_MAX, max(T_MIN, theta * shape(s)))


def zerox0_lifetime(s, lo=30 * DAY, hi=365 * DAY):
    """0x0.st: lo + (lo - hi) * (s/s_max - 1)^3."""
    return lo + (lo - hi) * (s / S_MAX - 1) ** 3


# ---- traffic -------------------------------------------------------------
def offered_gb_per_day(t, scenario):
    d = t / DAY
    if scenario == "steps":
        if d < 60:
            return 2
        if d < 120:
            return 10
        if 120 <= d < 123:
            return 40          # three-day flood
        if d < 150:
            return 10
        return 0.5
    if scenario == "cycle":
        return 6 * (1 + 0.8 * math.sin(2 * math.pi * t / DAY)) * (1 + 0.5 * math.sin(2 * math.pi * d / 7))
    if scenario == "flood":
        return 3 if not (100 <= d < 130) else 80
    if scenario == "quiet":
        return 0.3
    raise SystemExit(f"unknown scenario {scenario}")


def draw_size(rng):
    # heavy-ish tail: lognormal, median ~1.5 MB, clipped to S_MAX
    return min(S_MAX, max(1024, rng.lognormvariate(math.log(1.5 * MB), 1.4)))


MEAN_SIZE = None


def mean_size(rng, n=200000):
    return sum(draw_size(rng) for _ in range(n)) / n


# ---- simulation ----------------------------------------------------------
def run(mode, scenario, seed=1):
    rng = random.Random(seed)
    global MEAN_SIZE
    if MEAN_SIZE is None:
        MEAN_SIZE = mean_size(random.Random(99))

    expiring = [0.0] * (HORIZON + T_MAX + 2)   # bytes that expire at hour h
    used = 0.0
    a = 1 - 0.5 ** (1 / EWMA_HL)
    b_hat = None            # EWMA offered bytes/hour
    g_hat = None            # EWMA byte-weighted mean of shape(s)
    integ = 0.0
    theta = 30 * DAY        # starting time scale
    w_hat = [0.0] * len(BINS)   # EWMA byte share per size bin

    offered_total = refused_total = 0.0
    fills = []
    theta_log = []

    for t in range(HORIZON):
        used -= expiring[t]
        expiring[t] = 0.0
        if used < 1e-6:
            used = 0.0

        rate = offered_gb_per_day(t, scenario) * GB / DAY        # bytes/hour
        n = poisson(rng, rate / MEAN_SIZE)
        off_b = 0.0
        g_num = 0.0
        bin_b = [0.0] * len(BINS)
        for _ in range(n):
            s = draw_size(rng)
            off_b += s
            g_num += s * shape(s)
            bin_b[bin_of(s)] += s
            if mode == "fixed":
                T = zerox0_lifetime(s)
            else:
                T = lifetime(s, theta)
            if used + s > RHO_H * C:
                refused_total += s
                continue
            used += s
            expiring[t + max(1, int(round(T)))] += s
        offered_total += off_b

        # ---- controller (runs every hour, applies to the next hour) ------
        if mode == "look":
            b_hat = off_b if b_hat is None else b_hat + a * (off_b - b_hat)
            if off_b > 0:
                w_hat = [wh + a * (bb / off_b - wh) for wh, bb in zip(w_hat, bin_b)]
            if t % LOOK_EVERY == 0:
                # committed(tau): bytes already stored that are still alive at t+tau
                committed, k, acc = [], 0, used
                for h in range(1, TAU_GRID[-1] + 1):
                    acc -= expiring[t + h]
                    if h == TAU_GRID[k]:
                        committed.append(acc)
                        k += 1
                tot = sum(w_hat) or 1.0
                theta = solve_theta(committed, b_hat * LOOK_EVERY / LOOK_EVERY, [x / tot for x in w_hat])
        elif mode != "fixed":
            g = g_num / off_b if off_b > 0 else (g_hat or 1.0)
            b_hat = off_b if b_hat is None else b_hat + a * (off_b - b_hat)
            g_hat = g if g_hat is None else g_hat + a * (g - g_hat)
            rho = used / C
            e = RHO_T - rho
            if mode in ("ff", "ffb"):
                # Little's law: occupancy = inflow * byte-weighted lifetime
                theta_ff = RHO_T * C / max(b_hat * g_hat, 1.0)
            else:
                theta_ff = 30 * DAY
            if mode in ("fb", "ffb"):
                integ = max(-I_LIM, min(I_LIM, integ + KI * e))
                theta = theta_ff * math.exp(KP * e + integ)
            else:
                theta = theta_ff
            theta = max(THETA_LO, min(THETA_HI, theta))
        fills.append(used / C)
        theta_log.append(theta)

    return fills, theta_log, offered_total, refused_total


def poisson(rng, lam):
    if lam <= 0:
        return 0
    if lam > 50:
        return max(0, int(round(rng.gauss(lam, math.sqrt(lam)))))
    l, k, p = math.exp(-lam), 0, 1.0
    while True:
        p *= rng.random()
        if p <= l:
            return k
        k += 1


def spark(vals, lo=0.0, hi=1.0):
    bars = "▁▂▃▄▅▆▇█"
    out = []
    for v in vals:
        i = int((min(hi, max(lo, v)) - lo) / (hi - lo) * (len(bars) - 1) + 0.5)
        out.append(bars[i])
    return "".join(out)


def summarize(mode, scenario):
    fills, thetas, off, ref = run(mode, scenario)
    daily = [sum(fills[d * DAY:(d + 1) * DAY]) / DAY for d in range(HORIZON // DAY)]
    warm = fills[30 * DAY:]
    err = sum(abs(f - RHO_T) for f in warm) / len(warm)
    over = sum(1 for f in fills if f >= RHO_H - 0.005) / len(fills)
    peak = max(fills)
    one_mb_days = [lifetime(S_REF, th) / DAY for th in thetas[::DAY]] if mode != "fixed" else None
    print(f"{mode:6} fill  {spark(daily[::3])}")
    print(f"       mean|fill-target| {err:5.3f}   peak {peak:4.2f}   hours at cap {over*100:5.1f}%   refused {ref/off*100 if off else 0:5.1f}% of offered bytes")
    if one_mb_days:
        lo, hi = min(one_mb_days), max(one_mb_days)
        print(f"       1 MB file lifetime ranged {lo:5.1f} – {hi:5.1f} days")


if __name__ == "__main__":
    scen = sys.argv[1] if len(sys.argv) > 1 else "steps"
    print(f"scenario: {scen}   capacity {C/GB:.0f} GB   target {RHO_T}   hard {RHO_H}   (each bar = 3 days, ▁=0% █=100%)")
    print(f"offered  {spark([offered_gb_per_day(d*DAY, scen) for d in range(0, HORIZON//DAY, 3)], 0, max(offered_gb_per_day(d*DAY, scen) for d in range(HORIZON//DAY)))}  (GB/day, scaled)")
    for m in (sys.argv[2].split(",") if len(sys.argv) > 2 else ("fixed", "ff", "ffb", "look")):
        summarize(m, scen)

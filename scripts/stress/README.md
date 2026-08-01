# Stress tests

Long-running, invariant-checking load scripts for the KV store. These are
**not** Go tests and are **never run by CI** — they're for you to run by
hand (or on a schedule) when you want more confidence than the fast test
suite gives, e.g. before a release or after touching persistence / pub-sub
code.

Every script:
- builds the real `kv-server` binary (and `stressclient`, where used) fresh
- launches an isolated instance on its own ports and its own temp data dir
- runs a workload for a configurable duration/iteration count
- checks specific invariants **while running**, not just at the end
- prints `INVARIANT VIOLATION: <what> — <evidence>` and exits non-zero the
  moment something is wrong
- cleans up its server process and temp dir on exit (including on failure
  or Ctrl-C), via a `trap cleanup EXIT` in `common.sh`

Contrast with `../../integration/` (tagged Go tests): those are fast,
deterministic, single-pass correctness checks. These scripts are slow,
open-ended soak tests meant to *provoke* rare bugs (races, leaks,
timing-dependent data loss) by sustaining load, not to prove correctness
once.

## Quick start

Via the Makefile (defaults are smoke-test-scale — override for a real run):

```
make stress-readwrite       DURATION=20  CLIENTS=20
make stress-ttl-churn       DURATION=20  CLIENTS=20
make stress-pubsub-churn    DURATION=20  CLIENTS=20
make stress-contention      DURATION=20  CLIENTS=20
make stress-crash-cycles    ITERATIONS=10
make stress-all                                     # runs all five back to back
```

Or run a script directly — same values, positional instead of named:

```
./scripts/stress/run_readwrite_checkpoint.sh 300 50   # 300s, 50 clients
./scripts/stress/run_crash_cycles.sh 100               # 100 iterations
```

(`CLIENTS` is the Makefile's name for it; the scripts and `stressclient`
itself call the same thing "workers" internally — e.g. `[duration_seconds]
[workers]` below, or `stressclient`'s `-workers` flag. Same concept, just
named at the Makefile layer as `CLIENTS` since "worker" is really just
short for "simulated client".)

## Scripts

### `run_readwrite_checkpoint.sh [duration_seconds] [workers]`
Random SET/GET load across many concurrent clients, while a background loop
fires `CHECKPOINT` roughly once a second — racing sustained writes against
snapshot/journal-rotation. Each worker owns a disjoint key range, so a
read-your-writes check is unambiguous (no other worker can touch a key this
one is verifying).
**Invariant:** a write, once acknowledged, must still read back correctly
later — no write lost or clobbered by a concurrent checkpoint.

### `run_ttl_churn.sh [duration_seconds] [workers]`
Continuously sets keys with short (1–2s) TTLs and waits past each one's
deadline plus a grace window before checking it's gone.
**Invariant:** a key expires within a bounded window after its TTL elapses
— neither resurrected nor left alive indefinitely.

### `run_pubsub_churn.sh [duration_seconds] [workers]`
Subscribe → publish → wait for own message → unsubscribe, looped
continuously per worker/topic. After the workload stops, flushes each topic
with extra publishes (twice, with a pause) before checking the metrics
endpoint for `active_topics`/`active_subscribers` returning to 0 — the
double-flush accounts for `forwardMessages` only discovering a disconnected
client on its *next* write attempt (a well-known TCP quirk: the first write
after a peer closes can succeed once before the RST arrives).
**Invariant:** no leaked subscription/goroutine — accounting drains back to
zero after churn stops.

> **Known caveat — local ephemeral port exhaustion, not a server bug.**
> Every subscribe/unsubscribe cycle opens a brand-new TCP connection (that's
> deliberate — it's what's being stressed). At high `CLIENTS`/`DURATION`
> this can open more connections than your OS's ephemeral port range in a
> short window; each one sits in `TIME_WAIT` for `2×msl` after closing
> (macOS default: ~30s), so if connections open faster than old ones age
> out, the pool empties and further dials fail with
> `connect: can't assign requested address`. That's a client-side dial
> failure from local port exhaustion, not a KV-store correctness issue —
> check with `netstat -an -p tcp | grep -c TIME_WAIT` (macOS: pool size is
> `sysctl net.inet.ip.portrange.first/.last`, default 49152–65535 = 16384
> ports). If you hit this, wait ~30–60s for `TIME_WAIT` to drain, avoid
> running network-heavy phases back-to-back with no gap (e.g. via
> `stress-all`), or dial back `CLIENTS`/`DURATION` for this script
> specifically.

### `run_contention.sh [duration_seconds] [workers]`
Many clients incrementing one shared counter concurrently via `INCR`, then
verifies the final value equals the exact count of acknowledged increments.
There's no server-enforced lock/acquire-release primitive in this codebase
(only a `"lock-released:<key>"` pub/sub naming convention on `DEL`/TTL
eviction) — this exercises the closest real analogue: mutual exclusion via
the single-threaded event loop's serialization.
**Invariant:** zero lost updates under concurrent contention.

### `run_crash_cycles.sh [iterations]`
Bash-only (no `stressclient`): write a new key, `SIGKILL` the server,
restart against the same data directory, verify every previously-written
key survived, repeat. Uses raw RESP over `/dev/tcp` (see `common.sh`), not
the SDK, so it has no Go build step of its own beyond the server binary.
**Invariant:** every acknowledged write survives any number of hard-crash/
restart cycles.

## Shared infrastructure

- **`common.sh`** — sourced by every script. Builds `kv-server` and
  `stressclient` once, picks free ports, starts/stops the server, and
  provides raw-RESP helpers (`resp_cmd`, `get_value`) for the scripts that
  don't need the full SDK. Deliberately avoids `declare -A` (associative
  arrays) — stock macOS ships bash 3.2, which doesn't support them.
- **`stressclient/`** — a small Go program (`go run`, not a test binary)
  using the real SDK (`github.com/priyanshu-s-rana/kv_store/sdk`), exactly
  as a real application would. Implements the `readwrite`, `ttlchurn`,
  `pubsubchurn`, and `contention` workload modes; invoked by the bash
  scripts above, not run directly.

## Notes

- Each script picks its own ports and temp data directory, so they're safe
  to run concurrently with each other, with a dev server on the default
  port, or with the `integration/` test suite.
- None of this is wired into CI. If you want a subset running on a
  schedule (e.g. nightly), that's a separate, deliberate decision — these
  are slow by design and shouldn't gate every PR.

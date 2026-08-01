# Benchmarking

Tools for measuring KV store performance and deciding whether a change is
actually faster, not just differently noisy. Scripts live in `../scripts/`;
this directory holds run output (`benchmarks/<name>_<timestamp>/`, gitignored)
and this README (tracked).

## Suites

| Script | Load generator | Measures | Priority |
|---|---|---|---|
| `scripts/redis_benchmark.sh` | redis-benchmark | Isolated command throughput/latency: SET/GET/MSET/MGET/INCR/DECR/KEYS across concurrency, payload size, keyspace size, batch size, pipeline depth | Core |
| `scripts/memtier_benchmark.sh` | memtier_benchmark | Realistic mixed read/write workloads over a sustained duration — GC behavior, heap growth, latency drift | Macro |
| `scripts/startup_recovery_benchmark.sh` | redis-benchmark (seeding) + docker | Time from process start to accept-connections-ready: empty DB, snapshot-only recovery, snapshot+journal-replay recovery | Startup |
| `scripts/compare.sh` | — | Statistically-aware diff between two `redis_benchmark.sh` run directories | Comparison |

All three benchmark scripts share `scripts/lib/common.sh` for environment
metadata, statistics, and (for `redis_benchmark.sh`/`memtier_benchmark.sh`) the
rebuild/digest-verification logic described below.

## Why median, and what the noise threshold means

Every `(command, concurrency, payload, keyspace, batch, pipeline)`
configuration in `redis_benchmark.sh` runs multiple iterations (`--iterations`,
default 5), each a fresh `redis-benchmark` invocation. A single run's
throughput number is noisy — GC pauses, OS scheduling jitter, and thermal
throttling all move it around run to run. Reporting a single number invites
mistaking noise for a regression (or a regression for noise).

`summary.csv` reports the **median** as the headline number for each metric
(this is what the original column names — `rps`, `avg`, `p50`, etc. —
represent as of schema v2), because it isn't dragged around by one bad
iteration the way a mean is. Appended columns (`rps_mean`, `rps_stddev`, ...)
give the full distribution for anyone who wants it.

`compare.sh` uses those stddevs to decide whether a delta between two runs is
real: it computes each side's *relative* stddev (`stddev / mean`), takes the
larger one, doubles it, and calls that the noise threshold (as a percent). A
delta is flagged `REAL` only if `|delta%|` exceeds that threshold; otherwise
it's `NOISE`. This is a heuristic, not a statistical test — it's deliberately
simple so it's auditable from the CSV alone, and it scales the bar for "real"
by how noisy each run actually was rather than a single fixed percent.

## Running each suite

```bash
# Quick smoke test (~a few seconds, tiny requests/iterations, single
# concurrency/payload/keyspace) — good for checking the pipeline works
# before committing to a full run
./scripts/redis_benchmark.sh smoke-test --quick

# Full micro run (defaults: 5 concurrency levels x ~7 commands x 5
# iterations, plus payload/keyspace/batch/pipeline sweeps)
./scripts/redis_benchmark.sh main

# Macro run (skips gracefully with an install pointer if memtier_benchmark
# isn't installed)
./scripts/memtier_benchmark.sh main --ratio balanced --test-time 120

# Startup/recovery run (isolated docker compose project — does not touch
# your regular `docker compose up` dev instance or its data volume)
./scripts/startup_recovery_benchmark.sh main
```

A `Makefile` wraps all of the above — see `make help`-equivalent by reading the
`Makefile` directly, or just: `make bench-quick`, `make bench NAME=main`,
`make bench-macro NAME=main`, `make bench-recovery NAME=main`.

### Running everything under one directory

Each script computes its own timestamp by default, so three separate
invocations land in three separate `benchmarks/<name>_<ts>/` directories even
if you pass the same `<name>`. To get all three suites under a single shared
run directory, export the same `RUN_ID` to all of them — or just use:

```bash
make bench-all NAME=main
make bench-all NAME=main ARGS=--quick   # fast smoke across all three suites
```

This computes one timestamp, passes it as `RUN_ID` to all three scripts, and
continues past a failing suite rather than aborting the others (exits
non-zero at the end if any suite failed). Result:

```
benchmarks/main_<timestamp>/
├── benchmark_info.txt / summary.csv / failures.log / metrics_*.prom / ...   (micro)
├── c10/ c50/ c100/ c250/ c500/ payload/ keyspace/ batch/ pipeline/          (micro)
├── macro/
│   ├── benchmark_info.txt / summary.csv / memtier_raw.txt / metrics_*.prom
└── startup_recovery/
    ├── benchmark_info.txt / summary.csv
```

Every script accepts `-h`/`--help` for its full flag list.

## A/B comparison workflow

This is the primary use case: does branch `opt-parser` actually beat `main`?

```bash
git checkout main        && ./scripts/redis_benchmark.sh main
git checkout opt-parser  && ./scripts/redis_benchmark.sh opt-parser
./scripts/compare.sh benchmarks/main_<ts> benchmarks/opt-parser_<ts>
```

`redis_benchmark.sh` rebuilds the Docker image and force-recreates the container by
default — this guarantees you're never benchmarking a stale binary left over
from a previous branch. It also verifies the running container's image ID
matches what was just built, and aborts if they don't match. Use
`--no-rebuild` only when you've already confirmed the running container is
current (e.g. iterating on script flags without touching server code).

If the working tree is dirty, the script warns loudly and records
`Dirty Working Tree: true` in `benchmark_info.txt` (or aborts with
`--strict-clean`) — a dirty-tree result doesn't map to a single commit, so
don't trust it for a merge decision.

`compare.sh` joins the two runs' `summary.csv` on
`(Command, Concurrency, Payload, Keyspace, Batch, Pipeline)` and prints an
aligned table plus a `comparison.csv`. Pass `--fail-on-regression <pct>` to
exit non-zero when any `REAL` regression exceeds that percent — useful for
gating CI later, opt-in for now.

**For maximum rigor, interleave runs** (`main`, `branch`, `main`, `branch`)
rather than running them back-to-back, to average out thermal and
background-load drift across the comparison. The scripts don't automate
interleaving today — do it manually by re-running `redis_benchmark.sh` for each
side multiple times and comparing the run with the most representative
median, or by scripting the checkout/run loop yourself.

## Known limitations

- **No Zipfian key distribution.** `memtier_benchmark` supports uniform
  random, Gaussian, and sequential key patterns only — no Zipfian. If your
  production access pattern is Zipfian (hot-key skew), none of these
  suites will reproduce it faithfully.
- **Client and server run on the same host.** There's no network-latency
  isolation between load generator and server; both compete for the same
  CPU/memory. Absolute numbers won't match a real deployment, but this is
  constant across A/B runs, so *deltas* between `main` and a branch remain
  meaningful.
- **Docker network overhead is constant across runs** for the same reason
  — it adds a fixed offset to absolute latency, but doesn't bias a
  comparison between two runs both going through the same Docker setup.
- **No Go runtime/process metrics.** The server's Prometheus registry
  (`metrics/adapter.go`) doesn't register Go's runtime or process
  collectors, so heap/GC-pause numbers aren't available from `/metrics`
  today — resource-utilization capture is limited to `docker stats` and
  the server's own custom metrics.
- **Startup/recovery timing has ~50ms resolution**, measured by polling
  interval count rather than a high-resolution timestamp diff (kept
  portable across GNU/BSD `date`, which disagree on sub-second precision).
  Fine for snapshot/journal recovery at realistic dataset sizes; too coarse
  to distinguish empty-DB startup times from each other precisely.
- **Micro-suite dimension sweeps are faceted, not a full cartesian
  product.** Payload/keyspace/batch/pipeline are each swept independently
  against a representative concurrency level (rather than every
  combination of every dimension), to keep a full run tractable. The core
  sweep (all commands x all concurrency levels) always uses default
  payload/keyspace/pipeline values.

## Future expansion

- A custom Go load generator for mixed per-request payload shapes (the
  current suites use a single payload size per run; real traffic mixes
  sizes within a single workload).
- `benchstat` integration for the project's Go-level micro-benchmarks
  (`go test -bench`), separate from these end-to-end suites.
- Automating interleaved A/B runs (alternating `main`/`branch` within a
  single script invocation) instead of documenting it as manual practice.
- Wiring `compare.sh --fail-on-regression` into CI once the team has
  enough historical runs to pick a sane default threshold.

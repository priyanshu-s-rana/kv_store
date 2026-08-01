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
| `scripts/benchmark/runtime_benchmark.sh` | `go test -bench` (in-process, no server) | ns/op, B/op, allocs/op for the parser/store/persistence hot paths — *why* throughput moved, not just whether it did | Runtime |
| `scripts/compare.sh` | — | Statistically-aware diff between two `redis_benchmark.sh` run directories | Comparison |

All benchmark scripts share `scripts/lib/common.sh` for environment
metadata, statistics, and (for `redis_benchmark.sh`/`memtier_benchmark.sh`) the
rebuild/digest-verification logic described below.

## Throughput benchmarks vs. runtime benchmarks

The three suites above `runtime_benchmark.sh` in the table are **throughput
benchmarks**: they drive the real, compiled `kv-server` binary over a real
TCP connection (through Docker) and answer *"is this faster or slower, and
by how much, end to end?"* — the number that actually matters to a client.

`runtime_benchmark.sh` is a different kind of measurement: a **Go runtime
benchmark**, via `go test -bench -benchmem` against the hot-path packages
directly (`parser`, `store`, `persistence`) — in-process, no server, no
Docker, no network. It answers *"why"* a throughput number moved:

- Which component actually got faster — parsing, the store's command
  handlers, or persistence?
- Did allocations per operation (`allocs/op`, `B/op`) go up or down?
- Did a specific change (e.g. pooling a buffer, batching a copy) reduce
  allocation count the way it was supposed to?

A throughput regression with flat `ns/op`/`allocs/op` across all three
runtime-benchmarked packages points *away* from those packages — at the
network path, Docker, or the OS — the same way a throughput improvement
with no allocation change in any of them means the win came from somewhere
these benchmarks don't cover. Used together, a throughput delta plus a
runtime-benchmark delta locates *where* a change actually paid off, not
just that it did.

`runtime_benchmark.sh` deliberately runs `go test ./parser ./store
./persistence -bench=. -benchmem` — never `go test ./... -bench=.` — so
adding a benchmark to some other package doesn't silently get pulled into
this suite; see "Adding a new runtime benchmark" below.

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

# Go runtime benchmarks (ns/op, B/op, allocs/op) — no Docker, no server
./scripts/benchmark/runtime_benchmark.sh main
```

A `Makefile` wraps all of the above — see `make help`-equivalent by reading the
`Makefile` directly, or just: `make bench-quick`, `make bench NAME=main`,
`make bench-macro NAME=main`, `make bench-recovery NAME=main`,
`make bench-runtime NAME=main`.

### When to use `bench-runtime`

Reach for `bench-runtime` (or `runtime_benchmark.sh` directly) instead of, or
alongside, the throughput suites when:

- You changed code inside `parser`, `store`, or `persistence` and want fast
  (seconds, not minutes — no Docker rebuild) feedback before running a full
  `bench`/`bench-macro` pass.
- A throughput comparison (`compare.sh`) showed a delta and you need to know
  which component moved, or whether it's an allocation-count change versus
  pure CPU.
- You're checking a specific optimization claim — "parser pooling should
  drop `BenchmarkRESPParseSET`'s `allocs/op`", "snapshot capture should
  allocate less per key" — against the exact benchmark that exercises it
  (see `parser/benchmark_test.go`, `store/benchmark_test.go`,
  `persistence/benchmark_test.go`).

It's not a substitute for the throughput suites: in-process benchmarks never
exercise the network path, RESP framing over a real socket, Docker, or
concurrent-client contention — a `bench-runtime` win with no `bench`/
`bench-macro` movement means the bottleneck was never in these packages to
begin with.

### Running everything under one directory

Each script computes its own timestamp by default, so separate invocations
land in separate `benchmarks/<name>_<ts>/` directories even if you pass the
same `<name>`. To get all four suites under a single shared run directory,
export the same `RUN_ID` to all of them — or just use:

```bash
make bench-all NAME=main
make bench-all NAME=main ARGS=--quick   # fast smoke across all four suites
```

This computes one timestamp, passes it as `RUN_ID` to all four scripts, and
continues past a failing suite rather than aborting the others (exits
non-zero at the end if any suite failed). Result:

```
benchmarks/main_<timestamp>/
├── benchmark_info.txt / summary.csv / failures.log / metrics_*.prom / ...   (micro)
├── c10/ c50/ c100/ c250/ c500/ payload/ keyspace/ batch/ pipeline/          (micro)
├── macro/
│   ├── benchmark_info.txt / summary.csv / memtier_raw.txt / metrics_*.prom
├── startup_recovery/
│   ├── benchmark_info.txt / summary.csv
└── runtime/
    ├── benchmark_info.txt / benchmem.txt / benchmem_summary.csv
```

`make bench-all` runs all four suites this way. `bench-profile` (see below)
is deliberately excluded from `bench-all` and always writes to its own
`profile/` directory under a fresh run — it's diagnostic, not routine.

Every script accepts `-h`/`--help` for its full flag list.

## Profiling: `bench-profile`

`bench-runtime`'s `benchmem_summary.csv` tells you *that* something got
more/less expensive; it doesn't tell you *where* inside a benchmark the time
or allocations went. For that, generate pprof profiles from the same
benchmarks:

```bash
make bench-profile NAME=main
# or directly:
./scripts/benchmark/runtime_benchmark.sh main --profile
```

This reruns the identical `parser`/`store`/`persistence` benchmarks used by
`bench-runtime`, but under `-cpuprofile`/`-memprofile`/`-blockprofile`/
`-mutexprofile` instrumentation, writing to
`benchmarks/main_<timestamp>/profile/<package>/`:

- `cpu.pprof` — where CPU time went (`-cpuprofile`)
- `heap.pprof` / `allocs.pprof` — where allocations came from. Both files
  are the same underlying `-memprofile` data (Go only has one memory
  profiler; there's no separate "allocs-only" mode to invoke) — the split
  is in which pprof *view* you ask for at inspection time:
  `-inuse_space`/`-inuse_objects` for the heap.pprof use case (what's live
  now), `-alloc_space`/`-alloc_objects` for the allocs.pprof use case
  (cumulative allocation volume) — see below.
- `block.pprof` — goroutine blocking (channel/mutex wait) time
- `mutex.pprof` — mutex contention

It's a separate command rather than a `bench-all` step because profiling
changes what's being measured (instrumentation overhead skews `ns/op`) and
produces large binary artifacts that aren't useful to keep for every run —
run it on demand, when a `bench-runtime` number needs explaining.

### Inspecting a profile

```bash
go tool pprof -top benchmarks/main_<ts>/profile/store/cpu.pprof
go tool pprof -top -alloc_objects benchmarks/main_<ts>/profile/store/allocs.pprof
go tool pprof -top -inuse_space benchmarks/main_<ts>/profile/store/heap.pprof
go tool pprof -http=:8080 benchmarks/main_<ts>/profile/store/cpu.pprof   # interactive flame graph
```

### Comparing optimization branches

Combine `bench-runtime`/`bench-profile` with the existing A/B workflow
below: run `bench-runtime` on `main` and on your branch under separate
`NAME`s, then either diff `benchmem_summary.csv` by eye/spreadsheet (it's
small enough — typically ~10 rows) or feed both runs' `benchmem.txt` through
[`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) for a
statistically-aware comparison (not currently wired into these scripts — see
Future expansion):

```bash
git checkout main       && make bench-runtime NAME=main
git checkout opt-parser && make bench-runtime NAME=opt-parser
benchstat benchmarks/main_<ts>/runtime/benchmem.txt benchmarks/opt-parser_<ts>/runtime/benchmem.txt
```

If a runtime-benchmark delta shows up, pull `bench-profile` for the slower
side to see exactly which function or allocation site to look at next.

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

## Adding a new runtime benchmark

`runtime_benchmark.sh` runs exactly `go test ./parser ./store ./persistence
-bench=. -benchmem` — it doesn't discover benchmarks dynamically. To add
one:

1. Add the `func BenchmarkXxx(b *testing.B)` to the relevant package's
   `benchmark_test.go` (`parser/`, `store/`, or `persistence/`), following
   the existing benchmarks there: isolate the component under test, do
   setup before `b.ResetTimer()`, call `b.ReportAllocs()`.
2. If it's in a fourth package, add that package to the `PACKAGES` array
   near the top of `scripts/benchmark/runtime_benchmark.sh` — it's a
   deliberate allowlist, not `./...`, so a benchmark added anywhere else in
   the repo won't silently join this suite (or get missed if it's expected
   to).
3. `./scripts/benchmark/runtime_benchmark.sh smoke-test --quick` to confirm
   it's picked up and parses into `benchmem_summary.csv` correctly.

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
- **No Go runtime/process metrics from the live server.** The server's
  Prometheus registry (`metrics/adapter.go`) doesn't register Go's runtime
  or process collectors, so heap/GC-pause numbers for the *running server
  under load* aren't available from `/metrics` today — resource-utilization
  capture during `bench`/`bench-macro`/`bench-recovery` is limited to
  `docker stats` and the server's own custom metrics. `bench-runtime`/
  `bench-profile` don't fill this gap either: they measure the
  parser/store/persistence packages in an isolated `go test` process, not
  the live server's actual heap/GC behavior under real traffic.
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
- `benchstat` integration for `bench-runtime`'s output — right now
  comparing two `benchmem.txt` files is a manual `benchstat` invocation
  (see "Comparing optimization branches" above); wiring it into
  `runtime_benchmark.sh` or `compare.sh` directly would give a
  REAL/NOISE-style verdict the way `compare.sh` already does for the
  throughput suites.
- Automating interleaved A/B runs (alternating `main`/`branch` within a
  single script invocation) instead of documenting it as manual practice.
- Wiring `compare.sh --fail-on-regression` into CI once the team has
  enough historical runs to pick a sane default threshold.

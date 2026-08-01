# Runtime benchmark metrics

Reference for the numbers `make bench-runtime` / `make bench-profile` produce
(`benchmarks/<name>_<timestamp>/runtime/benchmem_summary.csv`, and the CI
workflows that run them — see `.github/workflows/ci.yml`,
`performance.yml`, `profile.yml`). For what the benchmarks
themselves cover and why they're organized the way they are, see
[`../../benchmarks/README.md`](../../benchmarks/README.md); this file is
just about reading the output.

## The three metrics

Every row in `benchmem_summary.csv` (and every `go test -bench -benchmem`
line) reports three numbers per operation:

| Metric | Meaning |
|---|---|
| `ns/op` | Wall-clock nanoseconds per call, averaged over however many iterations the benchmark ran (Go auto-scales iteration count to get a stable reading — see `Iterations`/`-benchtime` in `benchmarks/README.md`). |
| `B/op` | Bytes allocated on the heap per call, averaged the same way. |
| `allocs/op` | Number of distinct heap allocations per call. |

**Lower is better for all three.** `ns/op` is direct CPU cost. `B/op` and
`allocs/op` are indirect but often matter more under load: every heap
allocation is work the garbage collector has to do later, and GC pauses are
a shared-latency cost that shows up as tail-latency noise in the throughput
suites (`bench`, `bench-macro`) even on operations that didn't allocate
anything themselves. A change that drops `ns/op` by making a function do
more allocation isn't automatically a win — check `allocs/op` moved the
right way too.

`allocs/op` and `B/op` are usually the more actionable numbers day to day:
they're deterministic (don't depend on machine load, thermal state, or what
else is running on the CI runner) in a way `ns/op` isn't. Two runs of the
same benchmark on the same code can report different `ns/op` from run-to-run
noise; `allocs/op` for the same code essentially never changes run to run
unless the code path itself is non-deterministic (e.g. map iteration order
affecting which branch runs).

## Go microbenchmarks vs. redis-benchmark / memtier

This repository has two different kinds of performance measurement, and
they answer different questions — see `benchmarks/README.md`'s "Throughput
benchmarks vs. runtime benchmarks" section for the full explanation. Short
version:

| | Throughput suites (`bench`, `bench-macro`, `bench-recovery`) | Runtime benchmarks (`bench-runtime`) |
|---|---|---|
| Drives | The real, compiled `kv-server` binary, over a real TCP connection, through Docker | The `parser`/`store`/`persistence` packages directly, in-process |
| Answers | "Is it faster, end to end?" | "Why? Which component, and is it CPU or allocations?" |
| Load generator | redis-benchmark / memtier_benchmark (external tools) | `go test -bench` (Go's own testing package) |
| Sees | Network, RESP framing over a socket, Docker, concurrent-client contention, real GC behavior under sustained load | None of the above — pure function-call cost |

Neither replaces the other. A `bench-runtime` improvement with no
corresponding `bench`/`bench-macro` movement means the win is real inside
that package but isn't the current bottleneck end-to-end. A `bench`/
`bench-macro` regression with flat `bench-runtime` numbers across all three
packages means the regression is somewhere these benchmarks don't reach —
the network path, Docker, or the OS.

## Inspecting pprof profiles

`make bench-profile` reruns the same benchmarks under instrumentation and
writes, per package, to `benchmarks/<name>_<timestamp>/profile/<package>/`:

- `cpu.pprof` — where CPU time went
- `heap.pprof` / `allocs.pprof` — where allocations came from. These are
  the same underlying data (Go's `-memprofile` doesn't produce two separate
  profiles — there's no distinct "allocs-only" mode) with different pprof
  *views* applied at inspection time:
  - `go tool pprof -inuse_space` / `-inuse_objects` on `heap.pprof` — what's
    live right now (matches the "heap" mental model)
  - `go tool pprof -alloc_space` / `-alloc_objects` on `allocs.pprof` —
    cumulative allocation volume over the whole run (matches "allocs")
- `block.pprof` — goroutine blocking time (channel sends/receives, mutex
  waits)
- `mutex.pprof` — mutex contention specifically

Basic usage:

```bash
# top functions by CPU time
go tool pprof -top benchmarks/main_<ts>/profile/store/cpu.pprof

# top allocation sites by cumulative bytes allocated
go tool pprof -top -alloc_space benchmarks/main_<ts>/profile/store/allocs.pprof

# interactive flame graph in a browser
go tool pprof -http=:8080 benchmarks/main_<ts>/profile/store/cpu.pprof
```

In CI, download the `profile-<run-id>` artifact from the `profile.yml`
workflow run, unzip it, and run the same commands locally — profiles aren't
rendered inside the GitHub Actions summary itself (see that workflow's
summary for why).

## Interpreting results in practice

1. **Compare the same benchmark across two runs**, not different benchmarks
   against each other — `BenchmarkStoreMSet`'s absolute `ns/op` isn't
   meaningful next to `BenchmarkStoreGet`'s; they do different amounts of
   work per call by design (see `store/benchmark_test.go`).
2. **A single run's `ns/op` is noisy**; `allocs/op` usually isn't. If
   `allocs/op` is unchanged but `ns/op` moved a little, that's plausibly
   just machine noise (CI runners in particular share hardware with other
   jobs) — rerun before concluding anything. If `allocs/op` moved, that's a
   real code-path change and worth investigating regardless of how small
   the `ns/op` delta looks.
3. **A regression isolated to one package** (e.g. only `persistence`
   benchmarks moved) points you straight at what changed — check the diff
   against just that package first.
4. **A regression in `bench`/`bench-macro` with no `bench-runtime` movement**
   means look outside `parser`/`store`/`persistence` — networking, Docker,
   `server/`, or the OS/CI runner itself.
5. **When a number needs explaining, not just observing**, run
   `make bench-profile` for the specific package and pull up `-top` or
   `-http` on the relevant `.pprof` file rather than guessing from the
   summary numbers alone.

For how these benchmarks are structured, what each one specifically
measures, and how to add a new one, see
[`../../benchmarks/README.md`](../../benchmarks/README.md).

.PHONY: setup executable fmt vet build build-server build-cli install \
        test test-integration test-fuzz-seeds coverage \
        bench bench-quick bench-macro bench-recovery bench-runtime bench-profile \
        bench-all bench-compare \
        stress-readwrite stress-ttl-churn stress-pubsub-churn stress-contention \
        stress-crash-cycles stress-all

# Run once after cloning to activate the pre-commit hook.
setup: executable
	git config core.hooksPath .githooks
	@echo "hooks active: gofmt will run automatically on every commit"

# Ensures every shell script under scripts/ has its executable bit set.
# Cheap and idempotent — safe to depend on from any target that shells out
# to one of these scripts.
executable:
	chmod +x scripts/*.sh scripts/lib/*.sh
	chmod +x scripts/stress/*.sh 2>/dev/null || true
	chmod +x scripts/benchmark/*.sh 2>/dev/null || true

# Manual format — useful in CI or before opening a PR.
fmt:
	gofmt -w .

vet:
	go vet ./...

build:
	go build ./...

# Builds the kv-server and kv-cli binaries into the repo root (already
# gitignored — /kv-server, /kv-cli). For manually running/exploring the
# server yourself; the stress/bench scripts and `make test-integration`
# build their own throwaway copies separately and don't depend on this.
build-server:
	go build -o kv-server ./cmd/kv-server

build-cli:
	go build -o kv-cli ./cmd/kv-cli

# Installs both to $GOBIN (or $GOPATH/bin) so `kv-server` and `kv-cli` are
# just runnable by name from anywhere, same as `go install` normally works
# — different from build-server/build-cli above, which only build a local
# copy in this directory.
install:
	go install ./cmd/kv-server ./cmd/kv-cli

# Mirrors CI's race-detector test run (all packages, excludes the
# integration/fuzz suites below, which are opt-in and slower). -v gives
# per-test PASS/FAIL lines; awk echoes them live and tallies a final
# count. pipefail means the recipe still fails if go test fails, even
# though it's piped through awk.
test:
	@set -o pipefail; go test -race -count=1 -timeout 120s -v ./... 2>&1 \
		| awk '{ print; fflush() } /^--- PASS/{p++} /^--- FAIL/{f++} END{printf "\nPassed: %d  Failed: %d\n", p+0, f+0}'

# Same invocation as CI's coverage job; writes coverage.out and prints the
# per-package breakdown.
coverage:
	go test -count=1 -timeout 120s -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

# Process-level integration tests: build the real kv-server binary and drive
# it as a subprocess (real crashes via SIGKILL, real restarts, real config).
# Behind a build tag so `go test ./...` stays fast and CI-safe; run these
# explicitly and separately.
test-integration:
	go test -tags=integration -race -count=1 -timeout=180s -v ./integration/...

# Runs every fuzz target's seed corpus once (the same thing plain `go test
# ./...` already does for these packages — this target just makes it
# explicit and easy to run in isolation). Continuous fuzzing (`-fuzz=`) is a
# separate, manual, local-only activity — see the fuzz test file headers in
# parser/, persistence/, and store/ for the exact commands.
test-fuzz-seeds:
	go test -race -count=1 -run '^Fuzz' -v ./parser/... ./persistence/... ./store/...

# ── Benchmarks (see benchmarks/README.md) ────────────────────────────────
# NAME sets the run label (results land in benchmarks/<name>_<timestamp>/).
# Make variables are case-sensitive — it's NAME=, not name=, matching every
# variable below.
#
# Named knobs below translate straight to the right suite's flag, so
# bench-all NAME=baseline REQUESTS=1000000 CLIENTS=100 just works — no need
# to know which script owns which flag, or to hand-build an args string.
# Anything not covered by a named knob still goes through *_ARGS / ARGS
# (suite-specific ARGS win; ARGS alone applies to all three — --quick is
# the one flag every suite supports):
#   make bench-all NAME=baseline
#   make bench-all NAME=baseline REQUESTS=1000000 CLIENTS=100
#   make bench-all NAME=baseline ARGS=--quick
#   make bench-macro NAME=baseline ARGS="--ratio write-heavy"

NAME ?= dev
ARGS ?=
MICRO_ARGS ?= $(ARGS)
MACRO_ARGS ?= $(ARGS)
RECOVERY_ARGS ?= $(ARGS)
RUNTIME_ARGS ?= $(ARGS)

# Named knobs (all optional/blank by default — only added to the command
# line when set). ITERATIONS is shared: redis_benchmark.sh and
# startup_recovery_benchmark.sh both have an --iterations flag with the
# same meaning.
REQUESTS ?=
ITERATIONS ?=
CONCURRENCY ?=
CLIENTS ?=
THREADS ?=
TEST_TIME ?=
BENCH ?=
BENCHTIME ?=
COUNT ?=

MICRO_FLAGS := $(if $(REQUESTS),--requests $(REQUESTS)) \
               $(if $(ITERATIONS),--iterations $(ITERATIONS)) \
               $(if $(CONCURRENCY),--concurrency $(CONCURRENCY))
MACRO_FLAGS := $(if $(CLIENTS),--clients $(CLIENTS)) \
               $(if $(THREADS),--threads $(THREADS)) \
               $(if $(TEST_TIME),--test-time $(TEST_TIME))
RECOVERY_FLAGS := $(if $(ITERATIONS),--iterations $(ITERATIONS))
RUNTIME_FLAGS := $(if $(BENCH),--bench $(BENCH)) \
                  $(if $(BENCHTIME),--benchtime $(BENCHTIME)) \
                  $(if $(COUNT),--count $(COUNT))

# Smoke test: tiny requests/iterations, single concurrency/payload/keyspace.
# Good for checking the pipeline works before committing to a full run.
bench-quick: executable
	./scripts/redis_benchmark.sh $(NAME) --quick $(MICRO_FLAGS) $(MICRO_ARGS)

# Full micro run: redis-benchmark across concurrency, payload, keyspace,
# batch, and pipeline dimensions. Rebuilds and restarts the kv-server
# container by default.
bench: executable
	./scripts/redis_benchmark.sh $(NAME) $(MICRO_FLAGS) $(MICRO_ARGS)

# Macro run: memtier_benchmark mixed read/write workload. Skips gracefully
# (exit 0) if memtier_benchmark isn't installed.
bench-macro: executable
	./scripts/memtier_benchmark.sh $(NAME) $(MACRO_FLAGS) $(MACRO_ARGS)

# Startup/recovery timing: empty DB, snapshot-only, snapshot+journal-replay.
# Runs in an isolated docker compose project — does not touch the main
# kv-server container or its data volume.
bench-recovery: executable
	./scripts/startup_recovery_benchmark.sh $(NAME) $(RECOVERY_FLAGS) $(RECOVERY_ARGS)

# Go-level runtime benchmarks (ns/op, B/op, allocs/op) for the hot-path
# packages only (parser, store, persistence) — answers "why" a throughput
# change happened, complementing bench/bench-macro/bench-recovery above.
# No docker/server involved — just `go test -bench -benchmem` in-process.
# See benchmarks/README.md.
bench-runtime: executable
	./scripts/benchmark/runtime_benchmark.sh $(NAME) $(RUNTIME_FLAGS) $(RUNTIME_ARGS)

# Runs all four suites (micro, macro, startup/recovery, runtime) under ONE
# shared benchmarks/<name>_<timestamp>/ directory — computes a single
# timestamp and exports it as RUN_ID so each script reuses it instead of
# picking its own. Continues past a failing suite so one bad suite doesn't
# lose the others' results; exits non-zero at the end if any suite failed.
# Deliberately excludes bench-profile — pprof generation is diagnostic, not
# a routine part of every benchmark run; run it separately when needed.
bench-all: executable
	@run_id="$$(date +"%Y-%m-%d_%H-%M-%S")"; \
	out_dir="benchmarks/$(NAME)_$${run_id}"; \
	echo "Run directory: $${out_dir}"; \
	status=0; \
	echo "--- micro ---"; \
	RUN_ID="$$run_id" ./scripts/redis_benchmark.sh $(NAME) $(MICRO_FLAGS) $(MICRO_ARGS) || status=1; \
	echo "--- macro ---"; \
	RUN_ID="$$run_id" ./scripts/memtier_benchmark.sh $(NAME) $(MACRO_FLAGS) $(MACRO_ARGS) || status=1; \
	echo "--- startup/recovery ---"; \
	RUN_ID="$$run_id" ./scripts/startup_recovery_benchmark.sh $(NAME) $(RECOVERY_FLAGS) $(RECOVERY_ARGS) || status=1; \
	echo "--- runtime ---"; \
	RUN_ID="$$run_id" ./scripts/benchmark/runtime_benchmark.sh $(NAME) $(RUNTIME_FLAGS) $(RUNTIME_ARGS) || status=1; \
	echo; \
	if [ "$$status" -eq 0 ]; then \
		echo "All suites completed: $${out_dir}/"; \
	else \
		echo "One or more suites failed. See output above. Partial results: $${out_dir}/" >&2; \
	fi; \
	exit $$status

# Diagnostic-only: generates pprof profiles (cpu/heap/allocs/block/mutex)
# from the same Go runtime benchmarks bench-runtime uses, one set per
# hot-path package. NOT part of bench-all — profiling changes what's being
# measured (instrumentation overhead) and produces large binary artifacts,
# so it's opt-in, run on demand when a benchmark result needs explaining.
# See benchmarks/README.md for how to inspect the resulting profiles.
bench-profile: executable
	./scripts/benchmark/runtime_benchmark.sh $(NAME) --profile $(RUNTIME_FLAGS) $(RUNTIME_ARGS)

# Compares two redis_benchmark.sh run directories.
#   make bench-compare BASELINE=benchmarks/main_<ts> CANDIDATE=benchmarks/branch_<ts>
bench-compare: executable
	@if [ -z "$(BASELINE)" ] || [ -z "$(CANDIDATE)" ]; then \
		echo "Usage: make bench-compare BASELINE=benchmarks/main_<ts> CANDIDATE=benchmarks/branch_<ts> [ARGS='--fail-on-regression 5']"; \
		exit 1; \
	fi
	./scripts/compare.sh $(BASELINE) $(CANDIDATE) $(ARGS)

# ── Stress tests (see scripts/stress/) ───────────────────────────────────
# Long-running, invariant-checking load scripts — NOT run by CI, not part
# of `make test`. Each builds the real kv-server binary and stressclient
# once, drives a real subprocess instance, and exits non-zero with an
# "INVARIANT VIOLATION: ..." message on the first thing it catches.
#
# DURATION/CLIENTS/ITERATIONS are deliberately short defaults so a bare
# `make stress-<name>` finishes in well under a minute as a smoke check;
# override for an actual soak run, e.g.:
#   make stress-readwrite DURATION=300 CLIENTS=50
#   make stress-crash-cycles ITERATIONS=100

DURATION ?= 20
CLIENTS ?= 20
ITERATIONS ?= 10

# Random read/write load with checkpoints firing concurrently in the background.
stress-readwrite: executable
	./scripts/stress/run_readwrite_checkpoint.sh $(DURATION) $(CLIENTS)

# Continuous set-with-TTL churn, verifying expiry happens in a bounded window.
stress-ttl-churn: executable
	./scripts/stress/run_ttl_churn.sh $(DURATION) $(CLIENTS)

# Subscribe/publish/unsubscribe churn; asserts (via the metrics endpoint)
# that active topics/subscribers drain back to 0 — a goroutine/leak check.
stress-pubsub-churn: executable
	./scripts/stress/run_pubsub_churn.sh $(DURATION) $(CLIENTS)

# Many clients incrementing one shared counter concurrently; asserts no
# lost updates (event-loop serialization holds under load).
stress-contention: executable
	./scripts/stress/run_contention.sh $(DURATION) $(CLIENTS)

# Repeated write -> SIGKILL -> restart -> verify, several times in a row.
stress-crash-cycles: executable
	./scripts/stress/run_crash_cycles.sh $(ITERATIONS)

# Runs every stress script back to back. Continues past a failing script so
# one bad run doesn't hide the others; exits non-zero at the end if any
# script failed.
stress-all: executable
	@status=0; \
	echo "--- readwrite + checkpoint ---"; \
	./scripts/stress/run_readwrite_checkpoint.sh $(DURATION) $(CLIENTS) || status=1; \
	echo "--- ttl churn ---"; \
	./scripts/stress/run_ttl_churn.sh $(DURATION) $(CLIENTS) || status=1; \
	echo "--- pubsub churn ---"; \
	./scripts/stress/run_pubsub_churn.sh $(DURATION) $(CLIENTS) || status=1; \
	echo "--- contention ---"; \
	./scripts/stress/run_contention.sh $(DURATION) $(CLIENTS) || status=1; \
	echo "--- crash cycles ---"; \
	./scripts/stress/run_crash_cycles.sh $(ITERATIONS) || status=1; \
	if [ "$$status" -eq 0 ]; then \
		echo "All stress scripts completed with no invariant violations."; \
	else \
		echo "One or more stress scripts failed — see output above." >&2; \
	fi; \
	exit $$status

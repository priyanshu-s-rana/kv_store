#!/usr/bin/env bash
set -euo pipefail

# Converts one of the benchmark suites' summary.csv files (as produced by
# scripts/redis_benchmark.sh, memtier_benchmark.sh,
# startup_recovery_benchmark.sh, or scripts/benchmark/runtime_benchmark.sh —
# all of which share the "optional leading #schema=N comment, then a
# header row, then data rows" shape) into a GitHub-flavored markdown table
# on stdout. CI-presentation glue only — doesn't touch or duplicate any
# benchmark logic, just renders CSVs those scripts already produce.
#
# Usage:
#   csv_to_markdown.sh <csv-file>                    # every column, in file order
#   csv_to_markdown.sh <csv-file> "Col1,Col2,Col3"    # only named columns, in that order
#
# Prints a one-line markdown placeholder (not an error) if the file is
# missing or empty, since callers use this inline while building a
# best-effort summary — a missing CSV (e.g. a suite that failed before
# writing one) shouldn't break the rest of the summary.

csv_file="${1:?usage: csv_to_markdown.sh <csv-file> [Col1,Col2,...]}"
want_cols="${2:-}"

if [[ ! -s "$csv_file" ]]; then
    echo "_(no data: \`$csv_file\` not found or empty)_"
    exit 0
fi

# Strips any leading "#schema=N" comment line(s); every consumer of these
# CSVs (compare.sh, this script) treats '#'-prefixed lines as non-data.
data="$(grep -v '^#' "$csv_file")"

if [[ -z "$data" ]]; then
    echo "_(no data rows in \`$csv_file\`)_"
    exit 0
fi

if [[ -z "$want_cols" ]]; then
    awk -F',' '
        NR == 1 {
            line = "|"
            for (i = 1; i <= NF; i++) line = line " " $i " |"
            print line
            sep = "|"
            for (i = 1; i <= NF; i++) sep = sep "---|"
            print sep
            next
        }
        {
            line = "|"
            for (i = 1; i <= NF; i++) line = line " " $i " |"
            print line
        }
    ' <<< "$data"
else
    awk -F',' -v want="$want_cols" '
        BEGIN { nw = split(want, wantarr, ",") }
        NR == 1 {
            for (i = 1; i <= NF; i++) colidx[$i] = i
            line = "|"
            for (k = 1; k <= nw; k++) line = line " " wantarr[k] " |"
            print line
            sep = "|"
            for (k = 1; k <= nw; k++) sep = sep "---|"
            print sep
            next
        }
        {
            line = "|"
            for (k = 1; k <= nw; k++) {
                idx = colidx[wantarr[k]]
                line = line " " (idx ? $(idx) : "") " |"
            }
            print line
        }
    ' <<< "$data"
fi

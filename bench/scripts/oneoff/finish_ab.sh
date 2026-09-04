#!/usr/bin/env bash
# One command to turn a finished A/B run into a rescored results.json.
#
# WHY THIS EXISTS: baseline rows imported from an earlier run carry no shell
# counters, so `rescore` alone reports zeros for them. This backfills the
# counters first, then rescores.
set -euo pipefail
RUN="${1:?usage: finish_ab.sh <run-name>}"

# bench/ is two levels up from scripts/oneoff/.
BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$BENCH_DIR"

# shellcheck source=/dev/null
source .venv/bin/activate

echo "== backfill shell counters onto imported baseline rows =="
python scripts/oneoff/backfill_shellstats.py "results/$RUN"

echo "== rescore so results.json reflects the backfilled rows =="
python run_benchmark.py rescore "$RUN" --no-analysis || echo "(rescore failed; results.json left as written)"

# The HTML report step that used to live here pointed at a throwaway build.py
# outside the repo, which is long gone. Use `run_benchmark.py report` instead.
echo "== done. Build the report with: python run_benchmark.py report $RUN =="

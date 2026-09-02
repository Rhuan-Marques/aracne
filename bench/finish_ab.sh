#!/usr/bin/env bash
# One command to turn a finished A/B run into the published report.
set -euo pipefail
RUN="${1:?usage: finish_ab.sh <run-name>}"
cd /home/rhuan/repos/aracne/bench
source .venv/bin/activate
echo "== backfill shell counters onto imported baseline rows =="
python backfill_shellstats.py "results/$RUN"
echo "== rescore so results.json reflects the backfilled rows =="
python run_benchmark.py rescore "$RUN" --no-analysis || echo "(rescore failed; results.json left as written)"
echo "== build report =="
python /tmp/claude-1000/-home-rhuan-repos-aracne/cfabdc5c-8966-42f4-b02c-274d3f6702fe/scratchpad/report/build.py "$RUN"

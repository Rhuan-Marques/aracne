# Running the benchmark on SWE-Atlas

## Why this source exists

Every earlier run was scored on **bug fixes** — SWE-bench, SWE-bench-Live, Multi-SWE-bench.
A bug fix changes no declarations: across the nine `hard9` tasks, every gold patch changed
exactly **zero**. So `signature_changed` and `interface_conflict` — the one capability with no
substitute, since no grep tells you which call sites just went stale — could not fire, and
three runs measured a product with its most distinctive feature switched off by the task set.

SWE-Atlas is a **refactoring** benchmark. Its tasks move declarations by construction: the top
of the pool changes 256, 71, 60, 58 and 46 declarations. That is the sample where the warning
channel is exercised rather than assumed.

## Shape of the pool

70 tasks, of which **58** are in a language aracne scans. Filtered by how much the reference
solution actually moves:

| `--min-decls` | tasks | distinct repos |
|---|---|---|
| 20 | 11 | 4 |
| 10 | 22 | 4 |
| 0  | 58 | 7 |

**The repo count is the number that matters for statistics, not the task count.** Paired
analysis bootstraps over repositories (`Task.repo_slug`), because tasks in one repo share a
topology, a build system and a difficulty level. At `--min-decls 20` the effective N is 4
(grafana/grafana, grafana/k6, secdev/scapy, trufflesecurity/trufflehog), not 11. Widening to
the full 58 adds only Automattic/wp-calypso, renovatebot/renovate and one small repo.

## Pipeline

```bash
# 1. Inventory: what is there, and how much does each task move?
python bench/atlas_tasks.py --min-decls 20                    # print the table
python bench/atlas_tasks.py --min-decls 20 --out bench/samples/atlas.jsonl

# 2. Fixtures: pull each task image, copy its /workspace out, arac init + scan --all,
#    and COMMIT the aracne artifacts.
python bench/atlas_prepare.py --manifest bench/samples/atlas.jsonl

# 3. Snapshot, then run.
python bench/run_benchmark.py freeze --config configs/run/atlas-smoke.yaml
python bench/run_benchmark.py run    --config configs/run/atlas-smoke.yaml
```

`atlas_tasks.py` emits rows in the manifest shape `bench.sources.read_manifest` expects — the
top level is exactly `Task`'s fields and everything Atlas-specific rides in `raw` — so no other
phase needs to know where a task came from.

## Three things that are different from the SWE-bench sources, and why

**The tree comes out of the task image, never from a clone.** The verifier diffs the agent's
work against the image's `/workspace`, which is a squashed single commit carrying vendored
modules and generated files. Cloning upstream at `base_commit` would hand the agent a
different starting point than the one it is scored on. `arms.atlas_baseline_workdir` therefore
gives the control a *copy* of the prepared fixture with every aracne artifact deleted, so the
only difference between the arms is whether aracne is present.

**The aracne artifacts are committed, not left untracked.** The verifier captures the agent's
work with `git add -A && git diff --cached HEAD`; an untracked `.aracne/` would land in the
graded patch as thousands of lines of database.

**Grading is per task, not per batch.** An Atlas task is scored by its own verifier in its own
image, so there is no cross-instance harness to amortize. `reward` is 1.0 only when
`tests_reward == 1.0` **and** every must-have rubric item passes — a refactor that keeps the
tests green while ignoring the instruction is not a solve. Half the reward is a rubric graded
by an LLM, and `atlas_judge.py` serves the OpenAI-shaped endpoint the verifier expects on top
of `claude --print`, so judging bills the same subscription as the agent and needs no extra key.

Budget for it: the verifier runs the **full test suite twice** (baseline at base commit, then
patched) before it judges rubrics. On trufflehog that is upwards of 20 minutes per graded
patch.

## The failure mode to watch for

A `file` resource's id is the **absolute path it was scanned at**. Move or copy a prepared
fixture and every file id still names the old location, so the guard's `namesAnIndexedFile`
matches nothing: reads stop being intercepted, searches stop being annotated, and the run
silently measures plain Claude Code with an idle sidecar. Nothing errors — it looks exactly
like a model that chose not to use the tools.

`fixtures.topology_is_relocated()` checks for this and `arms.prepare_workdir` refuses the cell
rather than running it. If it fires, re-scan in place:

```bash
rm <worktree>/.aracne/topology.db <worktree>/.aracne/file_manifest.json
(cd <worktree> && arac scan --all)
python bench/run_benchmark.py freeze --config <your run config>
```

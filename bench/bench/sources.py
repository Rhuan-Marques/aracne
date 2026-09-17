"""Task sources, normalized into a single `Task`.

aracne supports Go, Python, JavaScript, TypeScript and Rust. The realistic, test-graded
tasks for each come from two external benchmarks:

  - Multi-SWE-bench (ByteDance-Seed/Multi-SWE-bench): Go, JS, TS, Rust (also Java/C/C++)
  - SWE-bench Lite   (SWE-bench/SWE-bench_Lite):       Python

Multi-SWE-bench has NO Python and SWE-bench is Python-only, so we use one source per
language and normalize both into a `Task`. Grading also differs per source and lives
in grade.py.

NOTE: Multi-SWE-bench is organized as per-language jsonl (c/cpp/go/java/js/rust/ts),
not a single dataset with a `language` column. The HF config/subset name per aracne
language is parameterized in config.yaml (`sources.multi_swe_bench.lang_config`);
confirm it against the dataset card. A `local_dir` override is supported for the case
where you have downloaded the jsonl files directly.
"""
from __future__ import annotations

import glob
import json
import os
import random
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from .atlas_prompt import sanitize

# aracne language -> source key
LANG_SOURCE = {
    "go": "multi_swe_bench",
    "javascript": "multi_swe_bench",
    "typescript": "multi_swe_bench",
    "rust": "multi_swe_bench",
    "python": "swe_bench",
}
ALL_LANGUAGES = list(LANG_SOURCE.keys())


@dataclass
class Task:
    key: str                 # stable unique id (used as the run identity)
    language: str            # aracne language: go/javascript/typescript/python
    source: str              # "multi_swe_bench" | "swe_bench"
    clone_url: str           # https://github.com/<org>/<repo>.git
    base_commit: str         # commit to check out before solving
    problem_statement: str   # the issue/PR text given to the agent
    raw: dict[str, Any] = field(default_factory=dict)  # original record (for predictions)

    def repo_slug(self) -> str:
        tail = self.clone_url.rstrip("/")
        if tail.endswith(".git"):
            tail = tail[: -len(".git")]
        parts = tail.split("/")
        return f"{parts[-2]}__{parts[-1]}"


# --------------------------------------------------------------------------- #
# Loading
# --------------------------------------------------------------------------- #

def _require_datasets():
    try:
        import datasets  # noqa: F401
        return datasets
    except ImportError as e:  # pragma: no cover - environment dependent
        raise SystemExit(
            "The 'datasets' package is required to load benchmark tasks.\n"
            "Install harness deps:  pip install -r bench/requirements.txt"
        ) from e


def _require_hf_hub():
    try:
        import huggingface_hub  # noqa: F401
        return huggingface_hub
    except ImportError as e:  # pragma: no cover - environment dependent
        raise SystemExit(
            "The 'huggingface_hub' package is required to load Multi-SWE-bench tasks.\n"
            "Install harness deps:  pip install -r bench/requirements.txt"
        ) from e


def _load_multi_records(language: str, cfg: dict) -> list[dict]:
    src = cfg["sources"]["multi_swe_bench"]
    lang_config = src["lang_config"].get(language)
    if lang_config is None:
        raise SystemExit(f"No Multi-SWE-bench config mapped for language '{language}'")

    local_dir = src.get("local_dir")
    if local_dir:
        # Local jsonl override: <local_dir>/<lang_config>/*.jsonl
        pattern = os.path.join(local_dir, lang_config, "*.jsonl")
        records: list[dict] = []
        for path in sorted(glob.glob(pattern)):
            with open(path, encoding="utf-8") as f:
                records.extend(json.loads(line) for line in f if line.strip())
        if not records:
            raise SystemExit(f"No jsonl records found under {pattern}")
        return records

    # Hub path. Multi-SWE-bench is organized as per-language DIRECTORIES of jsonl files
    # (c/ cpp/ go/ java/ js/ python/ rust/ ts/) and exposes only the 'default' config — so
    # `lang_config` is a directory name, NOT an HF builder config. Download that directory's
    # jsonl files directly and parse them (this also avoids datasets' nested-JSON schema
    # inference choking on heterogeneous records).
    hub = _require_hf_hub()
    api = hub.HfApi()
    try:
        all_files = api.list_repo_files(src["dataset"], repo_type="dataset")
    except Exception as e:  # noqa: BLE001
        raise SystemExit(f"Could not list {src['dataset']} on the HF Hub: {e}")

    prefix = f"{lang_config}/"
    lang_files = sorted(f for f in all_files if f.startswith(prefix) and f.endswith(".jsonl"))
    if not lang_files:
        raise SystemExit(
            f"No .jsonl files under '{prefix}' in {src['dataset']} "
            f"(check sources.multi_swe_bench.lang_config['{language}'])"
        )

    records = []
    for rel in lang_files:
        local = hub.hf_hub_download(src["dataset"], rel, repo_type="dataset")
        with open(local, encoding="utf-8") as f:
            records.extend(json.loads(line) for line in f if line.strip())
    return records


def _normalize_multi(r: dict, language: str) -> Task:
    org, repo, number = r.get("org"), r.get("repo"), r.get("number")
    key = r.get("instance_id") or f"{org}__{repo}_PR-{number}"
    base = r.get("base")
    if isinstance(base, dict):
        base_commit = base.get("sha") or base.get("ref") or ""
    else:
        base_commit = r.get("base_commit") or (base or "")
    problem = "\n\n".join(x for x in [r.get("title", ""), r.get("body", "")] if x).strip()
    return Task(
        key=key,
        language=language,
        source="multi_swe_bench",
        clone_url=f"https://github.com/{org}/{repo}.git",
        base_commit=base_commit,
        problem_statement=problem or f"Resolve {org}/{repo}#{number}",
        raw=r,
    )


# SWE-bench 5.x reads a row's EVAL metadata straight off the dataset (`image`, `eval_script`,
# `log_parser`, `eval_type`); the legacy `princeton-nlp/*` mirrors stop at the original 12
# columns, so the harness dies with `KeyError: 'image'` before it builds anything. The
# SWE-bench org republishes the SAME instance ids with those columns added, so remap the name
# instead of pinning an old harness. Loading and grading MUST agree, hence one helper.
_SWE_LEGACY_ORG = "princeton-nlp/"


def swe_dataset_name(src: dict) -> str:
    """The HF dataset the installed harness can actually evaluate."""
    name = src.get("dataset") or "SWE-bench/SWE-bench_Lite"
    if name.startswith(_SWE_LEGACY_ORG):
        return "SWE-bench/" + name[len(_SWE_LEGACY_ORG):]
    return name


def _load_swe_records(cfg: dict) -> list[dict]:
    src = cfg["sources"]["swe_bench"]
    datasets = _require_datasets()
    ds = datasets.load_dataset(swe_dataset_name(src), split=src.get("split", "test"))
    return [dict(r) for r in ds]


def _normalize_swe(r: dict) -> Task:
    repo = r["repo"]  # "owner/name"
    return Task(
        key=r["instance_id"],
        language="python",
        source="swe_bench",
        clone_url=f"https://github.com/{repo}.git",
        base_commit=r["base_commit"],
        problem_statement=r.get("problem_statement", "").strip(),
        raw=r,
    )


def _draw_repo_diverse(tasks: list[Task], samples: int, max_per_repo: int) -> list[Task]:
    """Draw `samples` tasks taking at most `max_per_repo` from any one repository.

    WHY. Tasks from one repo are not independent observations: they share a topology, a
    build system and a difficulty level, and the paired analysis bootstraps over
    REPOSITORIES, so a sample of 3 issues from vuejs/core has an effective N of 1, not 3.
    A plain shuffle-and-slice happily returns exactly that.

    Draws round-robin over repos — one task from each distinct repo before any repo gives up
    a second — so a truncated draw is still maximally diverse. `tasks` must already be
    shuffled; this preserves that order within each repo, keeping the draw deterministic.

    If the pool holds fewer distinct repos than `samples`, the caller gets a short list
    rather than a silently correlated one; cmd_sample reports the shortfall.
    """
    by_repo: dict[str, list[Task]] = {}
    for t in tasks:
        by_repo.setdefault(t.repo_slug(), []).append(t)

    drawn: list[Task] = []
    for depth in range(max(1, max_per_repo)):
        for repo_tasks in by_repo.values():   # dict order == first-seen == shuffled order
            if len(drawn) >= samples:
                return drawn
            if depth < len(repo_tasks):
                drawn.append(repo_tasks[depth])
    return drawn[:samples]


def load_tasks(languages: list[str], samples: int, sample_seed: int, cfg: dict,
               max_per_repo: int = 1) -> dict[str, list[Task]]:
    """Return {language: [Task, ...]} with a deterministic sample of `samples` per language.

    `max_per_repo` caps how many tasks may come from one repository (see
    `_draw_repo_diverse`). The default of 1 makes every sampled task an independent
    cluster, which is what the paired analysis needs.
    """
    out: dict[str, list[Task]] = {}
    swe_cache: list[Task] | None = None

    for lang in languages:
        source = LANG_SOURCE.get(lang)
        if source is None:
            raise SystemExit(f"Unsupported language '{lang}'. Supported: {ALL_LANGUAGES}")

        if source == "multi_swe_bench":
            tasks = [_normalize_multi(r, lang) for r in _load_multi_records(lang, cfg)]
        else:  # swe_bench (python)
            if swe_cache is None:
                swe_cache = [_normalize_swe(r) for r in _load_swe_records(cfg)]
            tasks = list(swe_cache)

        rng = random.Random(f"{sample_seed}:{lang}")
        rng.shuffle(tasks)
        out[lang] = _draw_repo_diverse(tasks, samples, max_per_repo)

    return out


def repo_counts(tasks: list[Task]) -> dict[str, int]:
    """Tasks per repository — the honest shape of a sample."""
    counts: dict[str, int] = {}
    for t in tasks:
        counts[t.repo_slug()] = counts.get(t.repo_slug(), 0) + 1
    return counts


# --------------------------------------------------------------------------- #
# Manifest (the stable, sampled task list shared by prepare/freeze/run)
# --------------------------------------------------------------------------- #

def manifest_path(samples_dir, sample_id: str) -> Path:
    """Path of the FINAL manifest jsonl (written by `prepare`, consumed by generate/freeze/run)."""
    return Path(samples_dir) / f"{sample_id}.jsonl"


def candidates_path(samples_dir, sample_id: str) -> Path:
    """Path of the oversampled CANDIDATE manifest (written by `sample`, pruned by `prepare`)."""
    return Path(samples_dir) / f"{sample_id}.candidates.jsonl"


def write_manifest(tasks_by_lang: dict[str, list[Task]], path: Path) -> None:
    """Write one JSON object per line, FLAT across languages, in language/sample order."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        for tasks in tasks_by_lang.values():
            for t in tasks:
                f.write(json.dumps({
                    "key": t.key,
                    "language": t.language,
                    "source": t.source,
                    "clone_url": t.clone_url,
                    "base_commit": t.base_commit,
                    "problem_statement": t.problem_statement,
                    "raw": t.raw,
                }) + "\n")


def read_manifest(path: Path) -> list[Task]:
    """Parse a manifest jsonl back into Tasks, preserving order."""
    path = Path(path)
    if not path.exists():
        raise SystemExit(
            f"No manifest at {path}.\n"
            "Run the 'sample' phase first:  python bench/run_benchmark.py sample --samples N"
        )
    tasks: list[Task] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        rec = json.loads(line)
        # SWE-Atlas instructions end with an interface specification -- every declaration the
        # reference solution adds or re-signs, with its file. Stripped HERE, at load, so every
        # manifest already on disk is covered, not only ones sampled after the change. See
        # bench/atlas_prompt.py for why the block must never reach the agent.
        if str(rec.get("source", "")).startswith("swe_atlas"):
            rec["problem_statement"], changes = sanitize(rec.get("problem_statement", ""), rec.get("key"))
            raw = rec.setdefault("raw", {})
            raw["prompt_interface_removed_chars"] = changes["interface_removed_chars"]
            raw["prompt_test_claim_removed"] = changes["test_claim_removed"]
            raw["prompt_paths_removed"] = changes["paths_removed"]
        tasks.append(Task(**rec))
    return tasks

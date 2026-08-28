#!/usr/bin/env python3
"""Back up (and later restore) every bench fixture's LLM-authored descriptions.

WHY. The fixture pool holds ~164k descriptions that cost many hours of generation. Every
path that preserves descriptions across a re-scan is keyed on the resource ID alone, so the
planned ID-scheme change would silently discard all of them, and `arac scan --hard` discards
them unconditionally. `arac descriptions export` writes an ID-independent sidecar; this
script drives it across the whole pool and — crucially — VERIFIES each sidecar can be
restored before anything destructive happens.

Sidecars are written to the FIXTURE ROOT (bench/fixtures/<key>/descriptions.*.jsonl), never
inside `worktree/`: fixtures.restore() runs `git clean -ffdxq` in the worktree before every
aracne run and would delete them.

Usage:
    python bench/migrate_descriptions.py export         # write sidecars for every fixture
    python bench/migrate_descriptions.py verify         # gate: re-import into a COPY, need 100%
    python bench/migrate_descriptions.py restore        # re-attach sidecars after a re-scan
    python bench/migrate_descriptions.py status         # what is described / backed up

Add `--fixture <substring>` to work on a subset, `--arac <path>` to pick the binary.
"""
from __future__ import annotations

import argparse
import json
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from pathlib import Path

FIXTURES = Path(__file__).resolve().parent / "fixtures"
# Both copies matter: `restore()` repopulates the worktree from `snapshot/` before every
# aracne run, so a worktree-only migration would be undone on the next run.
VARIANTS = ("worktree", "snapshot")


def sidecar_path(fixture_dir: Path, variant: str) -> Path:
    return fixture_dir / f"descriptions.{variant}.jsonl"


def db_path(fixture_dir: Path, variant: str) -> Path:
    return fixture_dir / variant / ".aracne" / "topology.db"


def fixtures(selector: str | None) -> list[Path]:
    """Fixtures to operate on. `selector` is a COMMA-SEPARATED list of substrings and a
    fixture matches if it contains ANY of them.

    A list rather than one substring because the sets that need normalizing are defined by
    the manifest, not by a shared name: the go/rust half of scale40 is 11 unrelated repos,
    and normalizing them one invocation at a time is how a pool drifts into three configs
    in the first place.
    """
    out = [d for d in sorted(FIXTURES.iterdir())
           if d.is_dir() and d.name != "_repos"
           and any(db_path(d, v).exists() for v in VARIANTS)]
    if selector:
        pats = [s.strip() for s in selector.split(",") if s.strip()]
        out = [d for d in out if any(p in d.name for p in pats)]
    return out


def described_count(db: Path) -> int:
    """How many resources carry a description. Read-only; never touches the live DB."""
    try:
        con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        try:
            return con.execute(
                "SELECT COUNT(*) FROM resources "
                "WHERE TRIM(COALESCE(description,'')) != ''").fetchone()[0]
        finally:
            con.close()
    except sqlite3.Error:
        return 0


def run_arac(arac: str, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run([arac, "descriptions", *args],
                          capture_output=True, text=True, timeout=1800)


def cmd_export(args) -> int:
    total, failures = 0, []
    for fx in fixtures(args.fixture):
        for variant in VARIANTS:
            db = db_path(fx, variant)
            if not db.exists():
                continue
            n = described_count(db)
            if n == 0:
                continue
            out = sidecar_path(fx, variant)
            res = run_arac(args.arac, "export", "--db", str(db), "--out", str(out))
            if res.returncode != 0:
                failures.append(f"{fx.name}/{variant}: {res.stderr.strip()[-200:]}")
                continue
            total += n
            print(f"  {fx.name}/{variant}: {n} -> {out.name}")
    print(f"\nExported {total} description(s) across {len(fixtures(args.fixture))} fixture(s).")
    for f in failures:
        print(f"  FAILED {f}", file=sys.stderr)
    return 1 if failures else 0


def cmd_verify(args) -> int:
    """The gate. Re-import each sidecar into a COPY of its own DB and require a full match.

    This must pass BEFORE the ID change lands: it proves the sidecar is a faithful backup
    while the IDs still line up, so a later shortfall can only mean the scheme change broke
    something — not that the backup was bad all along.
    """
    bad = []
    checked = 0
    for fx in fixtures(args.fixture):
        for variant in VARIANTS:
            db, side = db_path(fx, variant), sidecar_path(fx, variant)
            if not db.exists() or not side.exists():
                continue
            checked += 1
            with tempfile.TemporaryDirectory() as tmp:
                # Copy into a .aracne/ so the CLI's config lookup behaves as it would live.
                probe_dir = Path(tmp) / ".aracne"
                probe_dir.mkdir(parents=True)
                probe = probe_dir / "topology.db"
                shutil.copy2(db, probe)
                cfg = db.parent / "config.json"
                if cfg.exists():
                    shutil.copy2(cfg, probe_dir / "config.json")
                # Clear, then restore, and demand everything back.
                con = sqlite3.connect(probe)
                con.execute("UPDATE resources SET description = ''")
                con.commit()
                con.close()
                res = run_arac(args.arac, "import", "--db", str(probe),
                               "--in", str(side), "--min-rate", "1.0")
                got = described_count(probe)
                want = described_count(db)
                if res.returncode != 0 or got != want:
                    bad.append(f"{fx.name}/{variant}: restored {got}/{want} "
                               f"(exit {res.returncode})")
                    print(f"  FAIL {fx.name}/{variant}: {got}/{want}")
                else:
                    print(f"  ok   {fx.name}/{variant}: {got}/{want}")
    print(f"\nVerified {checked} sidecar(s); {len(bad)} failure(s).")
    for b in bad:
        print(f"  {b}", file=sys.stderr)
    if bad:
        print("\nDO NOT proceed with the ID change until every sidecar verifies.",
              file=sys.stderr)
    return 1 if bad else 0


def cmd_verify_scheme(args) -> int:
    """The gate that actually tests a SCHEME CHANGE.

    `verify` re-imports a sidecar into a copy whose IDs are unchanged, so every record
    matches at `exact_id` and the identity tier is never exercised — it proves the sidecar
    round-trips, not that it survives an id-scheme change. That blind spot hid two real
    bugs (a wrong root on one side of the match, and `file` resources whose loc_path is
    empty because the ID *is* the path).

    This applies the scheme-1 -> scheme-2 transform in SQL on a COPY (strip the leading
    "<projectdir>/" from every id, exactly as dropping filepath.Base(root) does), then
    imports the sidecar and requires the identity tier to recover everything. No rescan, so
    it runs over the whole pool in seconds instead of hours.
    """
    bad, checked = [], 0
    for fx in fixtures(args.fixture):
        for variant in VARIANTS:
            db, side = db_path(fx, variant), sidecar_path(fx, variant)
            if not db.exists() or not side.exists():
                continue
            checked += 1
            with tempfile.TemporaryDirectory() as tmp:
                probe_dir = Path(tmp) / ".aracne"
                probe_dir.mkdir(parents=True)
                probe = probe_dir / "topology.db"
                shutil.copy2(db, probe)
                cfg = db.parent / "config.json"
                if cfg.exists():
                    shutil.copy2(cfg, probe_dir / "config.json")
                want = described_count(db)
                changed = _rewrite_ids_scheme2(probe)
                res = run_arac(args.arac, "import", "--db", str(probe),
                               "--in", str(side), "--min-rate", str(args.min_rate))
                got = described_count(probe)
                tiers = " ".join(l.strip() for l in res.stdout.splitlines()
                                 if l.startswith("  ") and "unmatched sample" not in l)
                ok = res.returncode == 0 and got == want
                mark = "ok  " if ok else "FAIL"
                print(f"  {mark} {fx.name}/{variant}: {got}/{want} "
                      f"(ids changed: {changed}) {tiers}")
                if not ok:
                    bad.append(f"{fx.name}/{variant}: {got}/{want}")
    print(f"\nScheme-change gate: {checked} sidecar(s); {len(bad)} failure(s).")
    for b in bad:
        print(f"  {b}", file=sys.stderr)
    return 1 if bad else 0


def _rewrite_ids_scheme2(db: Path) -> int:
    """Apply the scheme-1 -> scheme-2 ID transform in place: drop the leading
    "<projectdir>/" that filepath.Base(root) used to prepend. Also rewrites the parent
    pointer inside properties_json, since that is an ID too.

    The prefix comes from the DB's own recorded root, NOT from the directory the copy sits
    in: a snapshot database is a copy of the worktree's, so its IDs carry the worktree's
    basename and keying off the directory name would silently rewrite nothing.
    """
    con = sqlite3.connect(db)
    row = con.execute("SELECT value FROM info WHERE key='root'").fetchone()
    if not row or not row[0]:
        con.close()
        return 0
    prefix = Path(row[0]).name + "/"
    changed = 0
    try:
        rows = con.execute("SELECT id, properties_json FROM resources").fetchall()
        existing = {r[0] for r in rows}
        for old, props in rows:
            new = old[len(prefix):] if old.startswith(prefix) else old
            if new != old and new in existing:
                # Stripping the prefix would collide with an id that already exists —
                # typically a `dependency` whose id is a raw import string that spells the
                # same thing as the module-qualified symbol. Report it; the real scanner
                # records these as resource-collision errors rather than crashing.
                print(f"      COLLISION {old} -> {new}")
                continue
            existing.add(new)
            newprops = props
            if props and prefix in props:
                newprops = props.replace(f'"{prefix}', '"')
            if new != old or newprops != props:
                con.execute("UPDATE resources SET id=?, properties_json=? WHERE id=?",
                            (new, newprops, old))
                changed += 1
        con.execute("UPDATE resources SET description=''")
        con.commit()
    finally:
        con.close()
    return changed


def cmd_restore(args) -> int:
    """Re-attach sidecars after a re-scan under the new ID scheme."""
    worst, failures = 1.0, []
    for fx in fixtures(args.fixture):
        for variant in VARIANTS:
            db, side = db_path(fx, variant), sidecar_path(fx, variant)
            if not db.exists() or not side.exists():
                continue
            report = fx / f"unmatched.{variant}.jsonl"
            res = run_arac(args.arac, "import", "--db", str(db), "--in", str(side),
                           "--min-rate", str(args.min_rate), "--report", str(report))
            line = next((l for l in res.stdout.splitlines() if "match rate" in l), "")
            print(f"  {fx.name}/{variant}: {line.strip()}")
            if res.returncode != 0:
                failures.append(f"{fx.name}/{variant}")
            try:
                worst = min(worst, float(line.split("match rate")[1].strip(" )%\n")) / 100)
            except (IndexError, ValueError):
                pass
    print(f"\nWorst match rate: {worst:.1%}; {len(failures)} fixture(s) below "
          f"--min-rate {args.min_rate}.")
    for f in failures:
        print(f"  BELOW THRESHOLD {f}", file=sys.stderr)
    return 1 if failures else 0


def cmd_rescan(args) -> int:
    """Re-scan each fixture worktree under the CURRENT id scheme, then re-freeze.

    Uses `arac scan --all` (FullReScan), never `--hard`: hard never reads the old database
    and drops every description. `--all` preserves them and, when the stored scheme is
    behind, remaps them by identity and records old->new in `resource_alias`.

    A snapshot holds only copies of `.aracne/` + config — there is no source under it — so
    it is re-derived from the worktree afterwards rather than scanned.
    """
    failures = []
    for fx in fixtures(args.fixture):
        wt = fx / "worktree"
        db = db_path(fx, "worktree")
        if not db.exists():
            continue
        before = described_count(db)
        res = subprocess.run(
            [args.arac, "scan", "--all", "--root", ".", "--output", ".aracne/topology.db"],
            cwd=str(wt), capture_output=True, text=True, timeout=7200)
        if res.returncode != 0:
            failures.append(f"{fx.name}: scan failed: {res.stderr.strip()[-200:]}")
            print(f"  FAIL {fx.name}: scan  {res.stderr.strip()[-120:]}")
            continue
        after = described_count(db)
        info = _db_info(db)
        # A handful of descriptions can legitimately fail to carry: a resource that no
        # longer exists (a misclassified dependency the import fix removed), or two
        # same-named members sharing a file and parent, which the matcher refuses to guess
        # between. Tolerate a small, REPORTED loss; fail on a real one. Aborting on a
        # single lost row left the worktree migrated and the snapshot stale — and since
        # restore() repopulates the worktree FROM the snapshot, that silently reverts the
        # fixture on its next use, which is worse than the loss it was guarding against.
        lost = before - after
        tolerance = max(5, int(before * 0.005))
        if lost > tolerance:
            failures.append(f"{fx.name}: descriptions {before} -> {after} (lost {lost})")
            print(f"  FAIL {fx.name}: descriptions {before} -> {after} (lost {lost} > {tolerance})")
            continue
        note = f" LOST {lost}" if lost > 0 else ""
        # Re-derive the snapshot from the freshly scanned worktree.
        snap = fx / "snapshot"
        copied = _refreeze(wt, snap)
        print(f"  ok   {fx.name}: desc {after}/{before}{note} "
              f"scheme={info.get('id_scheme','-')} aliases={info.get('aliases',0)} "
              f"refroze {copied} artifact(s)")
    print(f"\nRescanned {len(fixtures(args.fixture))} fixture(s); {len(failures)} failure(s).")
    for f in failures:
        print(f"  {f}", file=sys.stderr)
    return 1 if failures else 0


# The artifact set `bench/bench/fixtures.py` freezes. Kept in sync with ARACNE_ARTIFACTS
# there; a snapshot that misses one silently reverts it on the next run.
_ARTIFACTS = [".aracne", ".claude", ".opencode", ".mcp.json", "CLAUDE.md", "AGENTS.md"]


def _refreeze(worktree: Path, snapshot: Path) -> int:
    if snapshot.exists():
        shutil.rmtree(snapshot)
    snapshot.mkdir(parents=True, exist_ok=True)
    n = 0
    for rel in _ARTIFACTS:
        src = worktree / rel
        if not src.exists():
            continue
        dest = snapshot / rel
        if src.is_dir():
            shutil.copytree(src, dest)
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)
        n += 1
    return n


def _db_info(db: Path) -> dict:
    out = {}
    try:
        con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        try:
            row = con.execute("SELECT value FROM info WHERE key='id_scheme'").fetchone()
            if row:
                out["id_scheme"] = row[0]
            try:
                out["aliases"] = con.execute("SELECT COUNT(*) FROM resource_alias").fetchone()[0]
            except sqlite3.Error:
                out["aliases"] = 0
        finally:
            con.close()
    except sqlite3.Error:
        pass
    return out


def cmd_normalize(args) -> int:
    """Re-run `arac init` so every fixture carries the CURRENT shipped config, then re-freeze.

    Fixtures were pinned to whatever arac first touched them: `scaffold` only re-runs init
    when .mcp.json/.opencode.json is missing, so the pool accumulated four different
    `.aracne/config.json` variants. Two of them had `small_functions_visibility: full`,
    `include_incoming: true` and `blocked_tools: []` — i.e. NOT the shipped contract. An
    A/B over a mixture of configurations does not describe any single product, and it is
    what made an early read-size measurement (3.3x) describe a misconfigured fixture rather
    than aracne.

    Only config/integration files are touched; the topology database is left alone.
    """
    failures = []
    for fx in fixtures(args.fixture):
        wt = fx / "worktree"
        if not db_path(fx, "worktree").exists():
            continue
        for stale in (wt / ".aracne" / "config.json", wt / ".mcp.json"):
            stale.unlink(missing_ok=True)
        shutil.rmtree(wt / ".opencode", ignore_errors=True)
        def run_init() -> bool:
            for flag in ("--claude", "--opencode"):
                res = subprocess.run([args.arac, "init", flag, "-y"], cwd=str(wt),
                                     capture_output=True, text=True, timeout=600)
                if res.returncode != 0:
                    failures.append(f"{fx.name}: init {flag}: {res.stderr.strip()[-160:]}")
                    return False
            return True

        # First init writes a COMPLETE default config. It has to come first: EnsureConfig
        # replaces any file that does not parse as the full schema, so a partial config
        # written beforehand is silently discarded.
        if not run_init():
            print(f"  FAIL {fx.name}")
            continue
        if args.block:
            # Patch blocked_tools into the complete config, then init AGAIN so the generated
            # CLAUDE.md and permissions are derived from it. Writing the block after the last
            # init would leave the contract telling the agent to use native tools the guard
            # then denies — one wasted turn per attempt, the failure this started from.
            blocked = [t.strip() for t in args.block.split(",") if t.strip()]
            cfgp = wt / ".aracne" / "config.json"
            base = json.loads(cfgp.read_text())
            llm = base.setdefault("llm", {})
            # Set it on <any> AND on every per-harness block. A harness block wins over
            # <any> (Config.EffectiveAgent), so patching only <any> leaves an existing
            # `claude_code.main_agent` overriding it back to unblocked — which is how the
            # first attempt produced a config that said "blocked" and a contract that said
            # "your native grep still works".
            for scope in set(llm.keys()) | {"<any>"}:
                block = llm.setdefault(scope, {})
                if isinstance(block, dict):
                    block.setdefault("main_agent", {})["blocked_tools"] = blocked
            cfgp.write_text(json.dumps(base, indent=2))
            (wt / ".mcp.json").unlink(missing_ok=True)
            shutil.rmtree(wt / ".opencode", ignore_errors=True)
            if not run_init():
                print(f"  FAIL {fx.name}")
                continue
        _refreeze(wt, fx / "snapshot")
        print(f"  ok   {fx.name}: {config_fingerprint(wt)}")
    print(f"\nNormalized {len(fixtures(args.fixture))} fixture(s); {len(failures)} failure(s).")
    for f in failures:
        print(f"  {f}", file=sys.stderr)
    return 1 if failures else 0


def config_fingerprint(worktree: Path) -> str:
    cfg = worktree / ".aracne" / "config.json"
    if not cfg.exists():
        return "-"
    import hashlib
    return hashlib.sha256(cfg.read_bytes()).hexdigest()[:12]


def cmd_status(args) -> int:
    rows = []
    for fx in fixtures(args.fixture):
        for variant in VARIANTS:
            db = db_path(fx, variant)
            if not db.exists():
                continue
            side = sidecar_path(fx, variant)
            n_side = sum(1 for _ in side.open()) if side.exists() else 0
            rows.append((fx.name, variant, described_count(db), n_side))
    print(f"{'fixture':<46} {'variant':<9} {'in-db':>7} {'sidecar':>8}")
    for name, variant, n_db, n_side in rows:
        flag = "" if n_side >= n_db else "   <- NOT BACKED UP"
        print(f"{name:<46} {variant:<9} {n_db:>7} {n_side:>8}{flag}")
    print(f"\n{len(rows)} database(s); {sum(r[2] for r in rows)} description(s) in DBs, "
          f"{sum(r[3] for r in rows)} in sidecars.")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("command",
                    choices=["export", "verify", "verify-scheme", "rescan",
                             "normalize", "restore", "status"])
    ap.add_argument("--fixture", metavar="SUBSTR[,SUBSTR...]",
                    help="only fixtures whose directory name contains ANY of these "
                         "comma-separated substrings")
    ap.add_argument("--arac", default="arac", help="path to the arac binary")
    ap.add_argument("--block", metavar="TOOLS",
                    help="normalize: comma-separated native tools to hard-block "
                         "(e.g. read,grep,edit,write). Written before `arac init` so the "
                         "generated CLAUDE.md matches what the guard enforces.")
    ap.add_argument("--min-rate", type=float, default=0.99,
                    help="restore: fail below this match rate (default 0.99)")
    args = ap.parse_args()

    # A pattern that matches nothing is almost always a typo or a truncated paste, and the
    # consequence is silent: the tool reports success over the fixtures it DID match while
    # the ones you meant to fix stay drifted. That is how a config-uniformity fix "works"
    # and leaves the pool non-uniform. Fail loudly instead.
    if args.fixture:
        names = [d.name for d in fixtures(None)]
        unmatched = [pat.strip() for pat in args.fixture.split(",")
                     if pat.strip() and not any(pat.strip() in n for n in names)]
        if unmatched:
            print(f"--fixture: {len(unmatched)} pattern(s) matched no fixture:", file=sys.stderr)
            for pat in unmatched:
                print(f"  {pat}", file=sys.stderr)
            print(f"\n{len(fixtures(args.fixture))} fixture(s) would have been processed. "
                  f"Refusing: fix the pattern(s) or drop them.", file=sys.stderr)
            return 2

    return {"export": cmd_export, "verify": cmd_verify,
            "verify-scheme": cmd_verify_scheme, "rescan": cmd_rescan,
            "normalize": cmd_normalize,
            "restore": cmd_restore, "status": cmd_status}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())

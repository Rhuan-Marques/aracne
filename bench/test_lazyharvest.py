"""Lazily-generated descriptions must survive into the next run.

`descriptions.lazy` writes into the WORKTREE topology DB, and `fixtures.restore` copies the
snapshot's `.aracne` over that worktree before every cell. Without a harvest step the feature
would silently re-describe the same nodes on every cell of every run -- so these tests pin the
harvest, and specifically pin the one thing that must NOT be carried forward: a description
generated from source the agent had already patched.
"""
import json, sqlite3, sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from bench import fixtures


def _db(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    con = sqlite3.connect(str(path))
    con.execute("CREATE TABLE resources (id TEXT PRIMARY KEY, kind TEXT, name TEXT, "
                "language TEXT, description TEXT, properties_json TEXT, starts_at INT, "
                "ends_at INT, loc_path TEXT)")
    con.executemany("INSERT INTO resources (id,kind,name,description,loc_path) "
                    "VALUES (?,?,?,?,?)", rows)
    con.commit(); con.close()


def _desc(path):
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    out = dict(con.execute("SELECT id, description FROM resources").fetchall())
    con.close(); return out


class T:                      # a Task stand-in: harvest only needs these three
    key = "acme__widget-1"
    def repo_slug(self): return "acme__widget"
    base_commit = "abcdef1234567890"


def check(label, cond):
    """Assert and narrate. pytest collects these modules, so a failure must raise."""
    assert cond, label
    print("  PASS  " + label)
    return True


def test_harvest(tmp: Path):
    task, cfg = T(), {"gen_kinds": ["function"]}
    root = tmp / "fixtures"
    fx = root / "acme__widget@abcdef123456"
    snap = fx / "snapshot" / ".aracne" / "topology.db"
    wt = fx / "worktree" / ".aracne" / "topology.db"

    # snapshot: one curated description, three holes
    _db(snap, [("pkg.Kept", "function", "Kept", "curated prose", "a.go"),
               ("pkg.Hole", "function", "Hole", None, "a.go"),
               ("pkg.Edited", "function", "Edited", "", "patched.go"),
               ("pkg.Other", "function", "Other", "", "b.go")])
    # worktree: what a lazy run produced -- including one from a file the agent patched
    _db(wt, [("pkg.Kept", "function", "Kept", "lazy overwrite attempt", "a.go"),
             ("pkg.Hole", "function", "Hole", "lazily generated", "a.go"),
             ("pkg.Edited", "function", "Edited", "describes the SOLVED code", "patched.go"),
             ("pkg.Other", "function", "Other", "also lazy", "b.go")])
    (fx / "meta.json").write_text(json.dumps(
        {"key": "acme__widget@abcdef123456", "described": 1, "total": 4, "coverage": 0.25}))

    n = fixtures.harvest_descriptions(task, cfg, root, changed_paths={"patched.go"})
    got = _db_after = _desc(snap)
    ok = True
    ok &= check(f"harvested only the safe holes (got {n}, want 2)", n == 2)
    ok &= check("curated description not overwritten",
                got["pkg.Kept"] == "curated prose")
    ok &= check("hole filled from the lazy run", got["pkg.Hole"] == "lazily generated")
    ok &= check("description from a PATCHED file refused (no solution leak)",
                (got["pkg.Edited"] or "") == "")
    ok &= check("unrelated file harvested", got["pkg.Other"] == "also lazy")

    meta = json.loads((fx / "meta.json").read_text())
    ok &= check(f"meta coverage re-stamped (got {meta['coverage']}, want 0.75)",
                meta["coverage"] == 0.75)

    # conservative default: nothing known safe -> harvest nothing
    _db(tmp2 := tmp / "b" / "fixtures" / "acme__widget@abcdef123456" / "snapshot" / ".aracne" / "topology.db",
        [("pkg.Hole", "function", "Hole", None, "a.go")])
    _db(tmp / "b" / "fixtures" / "acme__widget@abcdef123456" / "worktree" / ".aracne" / "topology.db",
        [("pkg.Hole", "function", "Hole", "lazy", "a.go")])
    n2 = fixtures.harvest_descriptions(task, cfg, tmp / "b" / "fixtures", changed_paths=None)
    ok &= check(f"changed_paths=None harvests nothing (got {n2})", n2 == 0)

    ok &= check("patch_paths reads a unified diff",
                fixtures.patch_paths("--- a/x.go\n+++ b/x.go\n") == {"x.go"})
    return 0 if ok else 1




def test_survives_restore(tmp: Path) -> int:
    """The end-to-end claim: harvest -> restore -> the description is still there.

    restore() copies the snapshot's .aracne over the worktree, which is exactly what would
    erase a lazy description. This drives a real git worktree through it.
    """
    import subprocess
    ok = True
    repo = tmp / "repo"; repo.mkdir(parents=True)
    run = lambda *a: subprocess.run(a, cwd=str(repo), capture_output=True, text=True, check=True)
    run("git", "init", "-q"); run("git", "config", "user.email", "t@t"); run("git", "config", "user.name", "t")
    (repo / "a.go").write_text("package main\nfunc Hole() {}\n")
    run("git", "add", "-A"); run("git", "commit", "-qm", "base")
    sha = subprocess.run(["git", "rev-parse", "HEAD"], cwd=str(repo),
                         capture_output=True, text=True).stdout.strip()

    class RT:
        key = "acme__widget-1"
        base_commit = sha
        def repo_slug(self): return "acme__widget"
    task = RT()

    root = tmp / "fx"
    fx = root / f"acme__widget@{sha[:12]}"
    wtdir = fx / "worktree"
    wtdir.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["cp", "-r", str(repo), str(wtdir)], check=True)
    _db(fx / "snapshot" / ".aracne" / "topology.db",
        [("pkg.Hole", "function", "Hole", None, "a.go")])
    _db(wtdir / ".aracne" / "topology.db",
        [("pkg.Hole", "function", "Hole", "generated during the run", "a.go")])
    (fx / "meta.json").write_text(json.dumps({"key": fx.name, "coverage": 0.0}))

    cfg = {"gen_kinds": ["function"], "arac_bin": "/nonexistent-arac"}   # reindex is best-effort
    n = fixtures.harvest_descriptions(task, cfg, root, changed_paths=set())
    ok &= check(f"harvested the lazy description (got {n})", n == 1)

    fixtures.restore(task, cfg, root)      # the operation that used to erase it
    after = _desc(wtdir / ".aracne" / "topology.db")
    ok &= check("description survives restore() into the next cell",
                after.get("pkg.Hole") == "generated during the run")
    assert ok


if __name__ == "__main__":
    import tempfile
    with tempfile.TemporaryDirectory() as d:
        test_harvest(Path(d)); code = 0
        print()
        test_survives_restore(Path(d))
    print("\nRESULT:", "ALL PASS" if code == 0 else "FAILURES")
    raise SystemExit(code)

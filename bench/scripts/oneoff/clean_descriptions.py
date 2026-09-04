#!/usr/bin/env python3
"""Flag (and optionally blank) warmed descriptions that violate aracne house style.

Aracne descriptions must be ONE snappy, accurate line (see internal/prompts/describe_agent.go).
The headless warmer (bench/gen_descriptions.py) sometimes copies the resource's existing
docstring / doc-comment verbatim instead of summarizing — most visibly in Python (reST
docstrings with Examples/doctests) and, less often, Go (multi-line godoc, generated-mock
stubs). Those dumps are not representative of aracne-quality output and skew any benchmark
that leans on description quality.

This tool finds the offenders, and with --apply BLANKS them (description -> '') so the normal
warmer treats them as undocumented and regenerates them. Every touched topology.db is backed
up to `<db>.cleanbak` first; --restore puts the backups back.

Detection (a description is an offender if ANY holds):
  - contains a doc/doctest/reST/code marker  (>>>, ```` ``` ````, ``:func:``, ``.. math::``,
    an on-its-own-line Examples/Parameters/Returns/... section header, a leaked code line)
  - longer than --max-len characters                       (default 250)
  - spans --max-lines or more lines                        (default 3)

A short 2-line godoc copy that merely restates the identifier is a style nit, not a dump,
and regenerating it risks a worse result — those are deliberately NOT flagged.

Scope defaults to MANIFEST fixtures (those referenced by bench/samples/*.jsonl, matched the
same way the benchmark does — via clone_url, so pallets/flask is included); pass
--all-fixtures to sweep every fixture on disk.

Examples:
  python bench/clean_descriptions.py                      # dry-run, manifest fixtures
  python bench/clean_descriptions.py --apply              # blank offenders (backs up first)
  python bench/clean_descriptions.py --all-fixtures       # dry-run over everything on disk
  python bench/clean_descriptions.py --restore            # undo: restore *.cleanbak
Then regenerate the now-undocumented nodes:
  python bench/gen_descriptions.py                        # manifest fixtures, strengthened prompt
"""
from __future__ import annotations
import argparse, glob, json, re, shutil, sqlite3, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
KINDS = ("function", "method", "struct", "interface")

# Markers that betray a copied docstring / doc-comment (matched at ANY length).
_DOC_MARKER = re.compile(
    r""">>>                                   # python doctest prompt
      | ```                                    # fenced code block
      | ^\s*(Examples|Parameters|Returns|Raises|Yields|Notes|References|Explanation|Attributes|Args)\s*$  # rst/numpy section header on its own line
      | \.\.\s+(math|code|note|warning|\[)     # rst directive / footnote
      | :(func|class|meth|mod|attr|param|paramref|return|returns|rtype|raises|exc|data|ref|obj|const):  # sphinx role
    """,
    re.MULTILINE | re.VERBOSE,
)
# A leaked line of source code inside the description (indented block or bare statement).
_CODE_MARKER = re.compile(
    r"""\n[ \t]+\S                            # any indented continuation line (code block)
      | \bfunc\s+Test\w*\(                     # go test stub
      | \bfunc\s*\([^)]*\)\s*\w+\(             # go method signature
      | ^\s*(def|class|import|from|for|while|if|return|const|var|type)\b.*[:{(]  # py/go statement
    """,
    re.MULTILINE | re.VERBOSE,
)


def offense(desc: str, name: str, max_len: int, max_lines: int) -> list[str]:
    """Return the list of rule tags a description violates ([] means it's clean)."""
    d = desc.strip()
    if not d:
        return []
    nlines = d.count("\n") + 1
    tags = []
    if len(d) > max_len:
        tags.append(f"len>{max_len}")
    if nlines >= max_lines:
        tags.append(f"{nlines}lines")
    if _DOC_MARKER.search(d):
        tags.append("doc-block")
    if _CODE_MARKER.search(d):
        tags.append("leaked-code")
    return tags


def manifest_keys() -> set[str]:
    """Fixture keys referenced by bench/samples/*.jsonl, computed like the benchmark does
    (fixtures.fixture_key = repo_slug@base[:12], repo_slug derived from clone_url). Deriving
    from clone_url — not raw['org']/raw['repo'] — is what makes e.g. pallets/flask resolve."""
    keys: set[str] = set()
    for mp in glob.glob(str(HERE / "samples" / "*.jsonl")):
        if mp.endswith(".candidates.jsonl"):
            continue
        for line in open(mp, encoding="utf-8"):
            line = line.strip()
            if not line:
                continue
            try:
                r = json.loads(line)
            except json.JSONDecodeError:
                continue
            url = (r.get("clone_url") or "").rstrip("/")
            if url.endswith(".git"):
                url = url[:-len(".git")]
            parts = url.split("/")
            base = r.get("base_commit") or ""
            if len(parts) >= 2 and base:
                keys.add(f"{parts[-2]}__{parts[-1]}@{base[:12]}")
    return keys


def db_lang(conn) -> str:
    row = conn.execute(
        "SELECT language FROM resources GROUP BY 1 ORDER BY COUNT(*) DESC LIMIT 1").fetchone()
    return row[0] if row else "?"


def scan_db(db: str, max_len: int, max_lines: int):
    """Return (language, [(id, name, kind, desc, tags), ...]) of offenders in a topology.db."""
    conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        lang = db_lang(conn)
        ph = ",".join("?" * len(KINDS))
        rows = conn.execute(
            f"SELECT id,name,kind,description FROM resources "
            f"WHERE kind IN ({ph}) AND TRIM(COALESCE(description,'')) != ''", KINDS).fetchall()
    finally:
        conn.close()
    offenders = []
    for rid, name, kind, desc in rows:
        tags = offense(desc, name, max_len, max_lines)
        if tags:
            offenders.append((rid, name, kind, desc, tags))
    return lang, offenders


def blank(db: str, ids: list[str]) -> int:
    """Blank the given resource descriptions to '' so the warmer re-queues them. Returns count."""
    conn = sqlite3.connect(db)
    try:
        conn.execute("BEGIN")
        conn.executemany("UPDATE resources SET description='' WHERE id=?", [(i,) for i in ids])
        conn.commit()
    finally:
        conn.close()
    return len(ids)


def main() -> int:
    ap = argparse.ArgumentParser(description="Flag/blank non-house-style warmed descriptions")
    ap.add_argument("--fixtures-dir", default=str(HERE / "fixtures"))
    ap.add_argument("--only-manifest", dest="only_manifest", action="store_true", default=True,
                    help="scope to fixtures referenced by bench/samples/*.jsonl (default)")
    ap.add_argument("--all-fixtures", dest="only_manifest", action="store_false",
                    help="scan every fixture on disk")
    ap.add_argument("--languages", default="", help="comma list to restrict (e.g. go,python)")
    ap.add_argument("--max-len", type=int, default=250)
    ap.add_argument("--max-lines", type=int, default=3)
    ap.add_argument("--samples", type=int, default=3, help="offender examples to print per fixture")
    ap.add_argument("--apply", action="store_true", help="blank offenders (default is dry-run)")
    ap.add_argument("--restore", action="store_true", help="restore every <db>.cleanbak and exit")
    args = ap.parse_args()

    fx = Path(args.fixtures_dir)
    dbs = sorted(glob.glob(str(fx / "*/worktree/.aracne/topology.db")))

    if args.restore:
        n = 0
        for db in dbs:
            bak = db + ".cleanbak"
            if Path(bak).exists():
                shutil.copy2(bak, db)
                Path(bak).unlink()
                n += 1
        print(f"restored {n} topology.db from .cleanbak")
        return 0

    mkeys = manifest_keys() if args.only_manifest else None
    langs = {s.strip() for s in args.languages.split(",") if s.strip()}
    if args.only_manifest:
        print(f"scope: manifest fixtures ({len(mkeys)} keys)")

    grand_off = grand_desc = touched = 0
    per_lang: dict[str, list[int]] = {}
    for db in dbs:
        key = db.split("/")[-4]
        if mkeys is not None and key not in mkeys:
            continue
        lang, offenders = scan_db(db, args.max_len, args.max_lines)
        if langs and lang not in langs:
            continue
        # total described (for a % readout)
        conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        ph = ",".join("?" * len(KINDS))
        described = conn.execute(
            f"SELECT COUNT(*) FROM resources WHERE kind IN ({ph}) "
            f"AND TRIM(COALESCE(description,'')) != ''", KINDS).fetchone()[0]
        conn.close()
        if not offenders:
            continue
        grand_off += len(offenders)
        grand_desc += described
        acc = per_lang.setdefault(lang, [0, 0])
        acc[0] += len(offenders); acc[1] += described
        pct = 100 * len(offenders) / described if described else 0
        print(f"\n[{lang}] {key}: {len(offenders)}/{described} offenders ({pct:.1f}%)")
        for rid, name, kind, desc, tags in offenders[:args.samples]:
            first = desc.strip().splitlines()[0]
            print(f"    - [{kind}] {name}  {tags}\n        {first[:100]!r}")
        if args.apply:
            bak = db + ".cleanbak"
            if not Path(bak).exists():
                shutil.copy2(db, bak)
            blank(db, [o[0] for o in offenders])
            touched += 1

    print("\n" + "=" * 60)
    for lang in sorted(per_lang):
        o, d = per_lang[lang]
        print(f"  {lang:11} {o:5} offenders / {d:6} described ({100*o/d if d else 0:.1f}%)")
    print(f"  {'TOTAL':11} {grand_off:5} offenders / {grand_desc:6} described")
    if args.apply:
        print(f"\nAPPLIED: blanked {grand_off} descriptions across {touched} fixtures "
              f"(backups at <db>.cleanbak).")
        print("Next: python bench/gen_descriptions.py   # regenerate the blanked nodes")
    else:
        print("\nDRY-RUN. Re-run with --apply to blank these; --restore to undo later.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

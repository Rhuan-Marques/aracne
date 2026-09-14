#!/usr/bin/env bash
# Prepares the trial machine, then hands over to the shell.
#
# Everything here is about ONE thing: the container must be a machine that has never seen
# aracne, but not a machine that has never seen Claude Code -- otherwise the first ten
# minutes of every rehearsal are spent on a login flow and a theme picker instead of on
# the thing being tested.
set -euo pipefail

CLAUDE_HOME="$HOME/.claude"
mkdir -p "$CLAUDE_HOME"

# --- auth -------------------------------------------------------------------
# Copied in, never mounted into place: Claude Code rewrites this file when it refreshes
# an OAuth token, and a refresh inside a throwaway container must not reach back out and
# touch the host's credentials.
if [ -f /seed/credentials.json ] && [ ! -f "$CLAUDE_HOME/.credentials.json" ]; then
    cp /seed/credentials.json "$CLAUDE_HOME/.credentials.json"
    chmod 600 "$CLAUDE_HOME/.credentials.json"
    AUTH="seeded from the host"
elif [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    AUTH="ANTHROPIC_API_KEY"
else
    AUTH="NONE -- run 'claude' and log in, or pass -e ANTHROPIC_API_KEY"
fi

# --- onboarding ---------------------------------------------------------------
# Pre-accept onboarding and the per-directory trust dialog for the subjects. These are
# Claude Code's own first-run prompts; they are not what is under test, and answering
# them on every container start is noise.
#
# MERGED into whatever is already there, rather than written only when the file is
# absent: `claude --version` runs during the image build and creates ~/.claude.json, so a
# "skip if it exists" guard never fires and every container opens on the theme picker.
python3 - <<'PYCONF'
import json, os, glob
path = os.path.expanduser("~/.claude.json")
try:
    conf = json.load(open(path))
except Exception:
    conf = {}
conf["hasCompletedOnboarding"] = True
conf["autoUpdates"] = False
conf.setdefault("installMethod", "native")
projects = conf.setdefault("projects", {})
for sub in sorted(glob.glob(os.path.expanduser("~/subjects/*"))):
    entry = projects.setdefault(sub, {})
    entry["hasTrustDialogAccepted"] = True
    entry.setdefault("allowedTools", [])
json.dump(conf, open(path, "w"), indent=2)
PYCONF

# --- the cheat sheet ----------------------------------------------------------
if [ -t 1 ]; then
cat <<BANNER

  aracne trial machine  --  $(arac --version)
  claude $(claude --version 2>/dev/null | head -1)        auth: ${AUTH}

  Subjects (no .aracne/ anywhere yet):
$(cd "$HOME/subjects" && for d in */; do printf '    ~/subjects/%-14s %s\n' "${d%/}" "$(du -sh "$d" | cut -f1)"; done)

  The rehearsal, in order:
    cd ~/subjects/go-cobra
    arac init                 # the 7 questions. Escape must cancel writing nothing.
    ls -a .aracne .claude .claude/commands .claude/agents .claude/hooks
    grep -n Aracne CLAUDE.md  # the contract block
    arac node count && arac resource list --kind interface
    arac read <id> && arac grep flag
    arac check-updates        # clean -> exit 0
    echo '// x' >> command.go && arac check-updates ; echo "exit=\$?"   # stale -> exit 1
    git checkout command.go && arac update-file command.go
    arac cmd -- grep -rn Execute .     # aracne answers grep in all four modes
    arac cmd -- cat command.go         # cli mode: real cat. intercept_*: aracne
    claude                    # then make it read/grep/edit, and watch what it reaches for
    arac warnings list        # after the agent edits something
    git status --porcelain ; arac disable --all -y ; git status --porcelain

  Nothing here is persisted: --rm means the next run is a fresh machine.

BANNER
fi

exec "$@"

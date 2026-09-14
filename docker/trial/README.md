# The trial machine

A container that has never seen aracne, with Claude Code already installed and logged in.
It exists for one job: rehearse what a stranger gets.

```sh
docker/trial/trial.sh
```

That builds `arac` from the current working tree, installs Claude Code, clones four
subject projects — Go, Python, JavaScript, TypeScript — and opens a shell with the
rehearsal steps printed. Nothing persists: `--rm` and no volumes, so every run starts
from a machine with no `.aracne/`, no `~/.claude/settings.json` and no contract.

## What is deliberate

**The runtime stage has no Go toolchain and no aracne source.** A tester has neither. If
something only works because the repo is sitting next to it, this image is where that
shows up.

**Both builds are copied in** — `arac` and `arac-basic`. `arac-basic viz serve` must
refuse rather than pretend; that is a shipped artifact's behaviour, not a detail.

**`.git` is kept in the subject projects.** `git status --porcelain` before `arac init`
and after `arac disable --all` is how the uninstall path gets checked, and it is the one
a tester is most likely to need.

**Credentials are copied, never mounted into place.** Claude Code rewrites that file on a
token refresh; a throwaway container must not reach back and touch the host's.

## Installing the way a tester will

While the repo is private, the image builds from source — `go install` cannot reach it.
Once it is public and tagged, switch:

```sh
docker/trial/trial.sh --ref v0.1.0-rc.1
```

That runs the README's headline install line for real, gcc and all. Both paths should be
rehearsed before the soft launch: one compiles on the tester's machine, one does not, and
they fail differently.

## Auth

The wrapper mounts `~/.claude/.credentials.json` if it exists, else passes
`ANTHROPIC_API_KEY` if it is set. `--no-auth` starts with neither, which is worth doing
once: it is what a tester who has Claude Code but no subscription will hit, and
`descriptions.provider: "cli"` depends on it.

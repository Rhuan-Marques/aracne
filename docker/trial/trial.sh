#!/usr/bin/env bash
# Build the trial machine and drop into it.
#
#   docker/trial/trial.sh                 build from this working tree, open a shell
#   docker/trial/trial.sh --ref v0.1.0-rc.1   install from the public repo instead
#   docker/trial/trial.sh --no-auth       start with no credentials at all
#   docker/trial/trial.sh -- arac --version   run one command and exit
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="aracne-trial"
REF=""
SEED_AUTH=1
CMD=()

while [ $# -gt 0 ]; do
    case "$1" in
        --ref)     REF="$2"; shift 2 ;;
        --no-auth) SEED_AUTH=0; shift ;;
        --)        shift; CMD=("$@"); break ;;
        *)         echo "trial.sh: unknown argument $1" >&2; exit 2 ;;
    esac
done

VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
TAG="${IMAGE}:${REF:-local}"

echo "==> building ${TAG}  (version ${VERSION}${REF:+, go install @${REF}})"
docker build \
    -f "$ROOT/docker/trial/Dockerfile" \
    --build-arg "ARAC_VERSION=${VERSION}" \
    --build-arg "ARAC_REF=${REF}" \
    -t "$TAG" \
    "$ROOT"

# --rm, and no volumes: every run must start from a machine that has never seen aracne.
# That is the whole experiment, and a cached home directory would quietly end it.
RUN=(docker run -it --rm --hostname aracne-trial)

CREDS="$HOME/.claude/.credentials.json"
if [ "$SEED_AUTH" = 1 ] && [ -f "$CREDS" ]; then
    # :ro, and the entrypoint copies it inward -- a token refresh in here must not write
    # back to the host's real credentials.
    RUN+=(-v "${CREDS}:/seed/credentials.json:ro")
elif [ "$SEED_AUTH" = 1 ] && [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    RUN+=(-e "ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}")
fi

exec "${RUN[@]}" "$TAG" "${CMD[@]}"

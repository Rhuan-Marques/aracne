# syntax=docker/dockerfile:1
#
# A machine that has never seen aracne, with Claude Code already on it.
#
# The point of this image is the rehearsal: install aracne into a tree that has no
# .aracne/, no ~/.claude settings and no contract, run `arac init`, and find out whether
# the thing a stranger gets actually works. So the runtime stage carries the BINARY and
# nothing else -- no Go toolchain, no aracne source, no config -- because a tester has
# none of those either.
#
#   docker/trial/trial.sh          build + drop into a shell
#
# Two ways in, selected by ARAC_REF:
#   (unset)        build from the source in the build context  <- today, repo is private
#   v0.1.0-rc.1    go install github.com/Rhuan-Marques/aracne/cmd/arac@v0.1.0-rc.1
#                  <- what a tester will really run, once the repo is public and tagged.
#                     Switching to this is itself a test: it exercises the README's
#                     headline install line, gcc and all.

ARG GO_VERSION=1.25

########################################################################
# stage 1 -- build arac
########################################################################
FROM golang:${GO_VERSION}-bookworm AS build

# Not optional: the JS/TS, Rust and Java scanners are tree-sitter. The bookworm image
# already carries gcc; the alpine one would not.
ENV CGO_ENABLED=1
WORKDIR /src

ARG ARAC_REF=""
ARG ARAC_VERSION=dev

# Module cache first, so editing a .go file does not re-download the world.
COPY go.mod go.sum ./
RUN if [ -z "$ARAC_REF" ]; then go mod download; fi

COPY . .

# Both flavours, because both are shipped and they fail differently. The version is
# passed in rather than derived: .git is excluded from the build context (135M), and
# `go install` stamps its own.
RUN set -eux; \
    mkdir -p /out; \
    if [ -n "$ARAC_REF" ]; then \
        GOBIN=/out go install "github.com/Rhuan-Marques/aracne/cmd/arac@${ARAC_REF}"; \
        GOBIN=/out/basic go install -tags minimal "github.com/Rhuan-Marques/aracne/cmd/arac@${ARAC_REF}"; \
        mv /out/basic/arac /out/arac-basic; rmdir /out/basic; \
    else \
        go build -ldflags "-s -w -X main.Version=${ARAC_VERSION}" -o /out/arac ./cmd/arac; \
        go build -tags minimal -ldflags "-s -w -X main.Version=${ARAC_VERSION}" -o /out/arac-basic ./cmd/arac; \
    fi; \
    /out/arac --version; \
    /out/arac-basic --version

########################################################################
# stage 2 -- the trial machine
########################################################################
FROM debian:bookworm-slim AS trial

# python3 is a RUNTIME dependency and only for Python projects: that scanner drives the
# interpreter's own parser instead of a grammar. Everything else is in the binary.
# The rest is what a developer's box happens to have, and a slim image does not.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git python3 ripgrep jq less nano procps unzip \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/arac       /usr/local/bin/arac
COPY --from=build /out/arac-basic /usr/local/bin/arac-basic

ARG USERNAME=trial
ARG UID=1000
RUN useradd --create-home --uid ${UID} --shell /bin/bash ${USERNAME}
USER ${USERNAME}
WORKDIR /home/${USERNAME}

# Claude Code, by the same native installer a user would run. (npm fallback, if that URL
# ever moves: apt-get install nodejs npm && npm i -g @anthropic-ai/claude-code)
ENV PATH=/home/trial/.local/bin:/usr/local/bin:/usr/bin:/bin
RUN curl -fsSL https://claude.ai/install.sh | bash && claude --version

# Subject projects: one per language aracne claims, small enough to scan in seconds, and
# NOT aracne itself -- its own repo is already tuned and you know its shape. .git is kept
# on purpose: `git status --porcelain` before and after is how the uninstall gets checked.
RUN set -eux; mkdir -p subjects; \
    git clone --depth 1 https://github.com/spf13/cobra          subjects/go-cobra; \
    git clone --depth 1 https://github.com/psf/requests         subjects/py-requests; \
    git clone --depth 1 https://github.com/expressjs/express    subjects/js-express; \
    git clone --depth 1 https://github.com/sindresorhus/ky      subjects/ts-ky; \
    du -sh subjects/*

# Last, on purpose: this is the file that actually gets edited, and every layer above
# it is expensive -- a Claude Code download and four clones.
COPY --chmod=0755 docker/trial/entrypoint.sh /usr/local/bin/trial-entrypoint

ENTRYPOINT ["/usr/local/bin/trial-entrypoint"]
CMD ["bash", "-l"]

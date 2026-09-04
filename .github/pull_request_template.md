## What changed, and why

<!-- The "why" is the part that matters. What was wrong with the old behaviour? -->

## Checklist

- [ ] `make fmt` (note: `testing_ground/` is excluded on purpose)
- [ ] `make vet` — both tag sets
- [ ] `make test`
- [ ] `make test-minimal` — the Basic build is a shipped artifact
- [ ] New config keys documented in `docs/configuration.md`
- [ ] New feature flags added to `validConfig` in `internal/helper/config.go`

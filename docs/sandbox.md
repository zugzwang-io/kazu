# Claude cloud sandbox notes

Hard-won workarounds for working on kazu from Claude's cloud workspace (claude.ai sandbox sessions). GitHub Actions runners are **not** this restricted — these apply only to the sandbox. Carried from the pre-repo plan (2026-10-11); update as the sandbox changes.

## GitHub

- Attach the repo with `add_repo` (push access), then clone.
- GraphQL is blocked, so `gh pr create` fails; use REST: `gh api repos/zugzwang-io/kazu/pulls`. Creating, editing and closing PRs works.
- Deleting branches (push `--delete` or `DELETE .../git/refs`) is refused — leave a list for the maintainer.
- CI job logs (blob storage) are blocked: read step results with `gh api .../actions/jobs/<id>`, and make workflows print what matters as `::error` annotations. Check runs: `gh api repos/zugzwang-io/kazu/commits/<branch>/check-runs`.

## Go modules

- proxy.golang.org, golang.org/x and gopkg.in are blocked; GitHub works.
- Serve golang.org/x and gopkg.in modules from a local `file://` GOPROXY: zips built with `golang.org/x/mod/zip` from their GitHub mirrors (a module named `tool` won't build — reserved word in Go 1.24), then `GOPROXY=file://...,direct GOSUMDB=off GOTOOLCHAIN=local go mod tidy`.
- `gopkg.in/yaml.v3` also needs `gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405` (from `go-check/check` branch v1).
- Pin versions that support the repo's Go version (pgx v5.7.5; newer needs Go 1.25).
- Never hand-write `.mod` files for go.sum — a lost trailing newline cost a CI round.

## Lint

Download the golangci-lint release binary from GitHub (version pinned in `.github/workflows/pr.yml`) and run it with the repo's `.golangci.yml`. Note: `exhaustive` wants a `default:` on switches over other packages' enums (such as `yaml.Kind`).

## No Docker daemon

- Postgres 16 binaries are at `/usr/lib/postgresql/16/bin` (run as the `claude` user) — use them for dbtest-style tests.
- k6 can be downloaded from GitHub releases.
- Images and compose run only in CI; don't attempt the smoke flow in the sandbox.

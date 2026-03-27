# TODOs

## Playwright E2E in CI

**What:** Add a GitHub Actions workflow step that runs Playwright E2E tests on push/PR.

**Why:** Playwright tests exist locally but don't run in CI. Without CI integration, regressions in auth flows (login, logout, setup, cookie handling) won't be caught until manual testing.

**Context:** The SvelteKit load refactor (2026-03-26) added Playwright E2E tests covering the login, setup, logout, and navigation flows. These tests require a running Go binary (Bastion server + SQLite) as the test backend. The CI workflow needs to:
1. Build the Go binary with embedded frontend (`go build ./cmd/bastion`)
2. Start it with a temp config (`:0` port, in-memory or temp SQLite)
3. Wait for health check (`/health` endpoint)
4. Run `npx playwright test` with `baseURL` pointing to the started server
5. Stop the server and collect test artifacts

**Depends on / blocked by:** Playwright tests must be written and passing locally first (part of the load refactor PR).

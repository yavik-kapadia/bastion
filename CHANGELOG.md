# Changelog

All notable changes to Bastion will be documented in this file.

## [0.2.9] - 2026-05-22

### Fixed
- **Dashboard thumbnail no longer inflates `subscriberCount`.** Each open dashboard tab was polling `/api/v1/streams/<name>/thumbnail` every 15s, and the handler shelled out to ffmpeg as a real SRT request-mode subscriber — so N tabs ≈ N extra "viewers" in the reported count. The handler now dedupes concurrent requests with singleflight and caches the most recent PNG for 10s, so regardless of how many tabs are open the upstream SRT subscription fires at most once per stream per 10s.

### Added
- Tests: `TestThumbnailCacheSingleflight`, `TestThumbnailCacheTTL`, `TestThumbnailCacheErrorNotCached`, `TestThumbnailCacheDifferentStreamsIndependent`.

## [0.2.6] - 2026-04-30

### Fixed
- **Relay: publisher reconnect race could crash the relay or strand viewers.** The previous `publisher != nil` check let a reconnecting publisher attach while the prior session was still tearing down — fan-out to a subscriber whose channel had just been closed could panic, and viewers could end up wired to a stale session. `Stream` now uses an explicit `pubIdle/pubActive/pubDraining` state machine; new publishers wait until the prior session's writePumps have drained.
- **Relay: subscriber slot accounting under publisher disconnect.** Each subscriber now signals exit via a `done` channel that `relayLoop`'s teardown waits on (bounded to 5s), guaranteeing `subscriberCount` returns to truth even if a writePump is wedged. Also defends fan-out against send-on-closed-channel with a recover() guard.
- **Relay: `r.streams` no longer accumulates zombie entries.** Stream entries with no publisher and no subscribers are removed from the map after the publisher's session ends.

### Added
- Tests: `TestPublisherEOFReconnectCleansSubscribers`, `TestPublisherEOFRaceWithReconnect`, `TestStreamGCAfterPublisherExits` cover the publisher-disconnect-with-attached-subscribers scenarios that produced the v0.2.4 freeze.

## [0.2.0] - 2026-03-26

### Changed
- **Auth: HTTP-only cookies replace localStorage tokens.** Login and setup set a `bastion_session` HttpOnly cookie. The auth token is never exposed to JavaScript, eliminating XSS token theft. Bearer token auth preserved for external API consumers.
- **Frontend: SvelteKit load functions.** All data fetching moved from `onMount` to `+page.ts`/`+layout.ts` load functions. Pages render with data already available (no loading spinners).
- **Frontend: Svelte 5 runes.** Stores migrated from `writable`/`derived` to `$state`/`$derived`. Components use `$props()`, `$effect()`, `$derived()`. Store files renamed to `.svelte.ts`.
- **Auth flow: `/auth/me` replaces setup-status on every page load.** Layout calls `GET /auth/me` once to validate the session cookie. Setup-status check moved to login page only.
- **WebSocket auth via cookie.** Browser sends the session cookie automatically on WS upgrade. No `?token=` query parameter needed.
- **Thumbnail auth via cookie.** `<img>` tags no longer need `?token=` in the src URL.
- **API rename: `/auth/bootstrap` → `/auth/setup`.** Matches the `/setup` frontend route.
- `public_host` now returned from `/auth/me` instead of stored in sessionStorage.
- CORS allowed headers include `X-Requested-With` for CSRF protection.

### Added
- `GET /api/v1/auth/me` endpoint: returns current user info from session cookie or Bearer token. Includes `public_host`.
- `POST /api/v1/auth/logout` endpoint: clears the session cookie.
- CSRF protection: state-changing requests via cookie require `X-Requested-With` header. Bearer-token requests are exempt.
- Cookie `Secure` flag auto-detected from TLS status (works for HTTP VPN and HTTPS).
- `AuthError` class in `api.ts` for 401 handling.
- `export const ssr = false` in root layout for static SPA compatibility.
- 16 new Go tests: cookie auth, `/auth/me`, `/auth/logout`, `/auth/setup`, and CSRF enforcement.

### Removed
- `localStorage` token storage (replaced by HttpOnly cookie).
- `sessionStorage` for `publicHost` (replaced by `/auth/me` response).
- `?token=` query parameter on WebSocket and thumbnail requests.
- Dead frontend code: `api.createAPIKey()`, `api.globalMetrics()` (never called from any component).
- `token` field from login/setup API response body (token is now cookie-only).

## [0.1.4] - 2026-03-26

### Added
- Three-tier host URL resolution for SRT commands: user override (localStorage) → server `public_host` config → `window.location.hostname` auto-detect
- `public_host` field in `[api]` config section for Cloudflare tunnel deployments where the HTTP hostname differs from the SRT host
- `public_host` included in login and bootstrap API responses so the frontend can resolve the correct SRT host
- `resolvedHost` derived store that auto-populates SRT commands with the correct hostname (zero config for direct IP and WireGuard)
- TODOS.md for tracking deferred work

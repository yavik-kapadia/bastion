# Changelog

All notable changes to Bastion will be documented in this file.

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

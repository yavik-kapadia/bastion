# Changelog

All notable changes to Bastion will be documented in this file.

## [0.1.4] - 2026-03-26

### Added
- Three-tier host URL resolution for SRT commands: user override (localStorage) → server `public_host` config → `window.location.hostname` auto-detect
- `public_host` field in `[api]` config section for Cloudflare tunnel deployments where the HTTP hostname differs from the SRT host
- `public_host` included in login and bootstrap API responses so the frontend can resolve the correct SRT host
- `resolvedHost` derived store that auto-populates SRT commands with the correct hostname (zero config for direct IP and WireGuard)
- TODOS.md for tracking deferred work

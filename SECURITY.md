# Security policy

## Supported versions

Only the latest release (`stable` image tag) gets security fixes.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private reporting instead:
**Security → Report a vulnerability** in this repository.

Include what you found, how to reproduce it and the version (`/health` or the panel footer). You will get an answer within a week.

## What Plexmor protects

- **Addon URLs are secrets.** Each user's URL contains a random key; anyone with it can use that user's access to Plex. It is never logged, and **Generate a new address** in the panel revokes it.
- **Plex tokens** are stored encrypted (AES-GCM) in `/data/accounts.json`, never logged, and with the proxy modes never leave Plexmor.
- **Panel:** sign-in with Plex (PIN tied to the browser that started it), signed `HttpOnly`/`SameSite=Lax` session cookies, CSRF checks, a strict Content-Security-Policy and no third-party resources.
- **Container:** distroless image, non-root user, read-only root filesystem and no capabilities in the provided compose file.

Run Plexmor behind HTTPS (a reverse proxy) when it is reachable from the internet.

### shelf: preview of feature/login (hello--feature-login)

**Ready** at commit `3f2a9c1e5b7d`.
Open it with `shelf app open hello --preview feature/login`.

**Tests:**
- smoke passed

**Verification:** verified, signed by keyless, issuer https://token.actions.githubusercontent.com, subject ^https://github\.com/tweinmann/shelf/\.github/workflows/(build|release)\.yml@refs/(tags|heads)/.+$.

**Scan** (trivy 0.75.0, 2026-10-28T13:00:00Z):

| Component | Critical | High |
|---|---|---|
| smoke | 0 | 0 |
| web | 0 | 4 |

**Compared with hello at commit `42a964eb0b4b`:**

| Component | Image | Critical | High |
|---|---|---|---|
| smoke | new | 0 | 0 |
| web | changed | 0 (was 1) | 4 (was 26) |

All images: 0 critical, 4 high (hello: 1 critical, 26 high).

**Isolation:** Pod Security baseline; reaches its own components, DNS, the internet and no private addresses.

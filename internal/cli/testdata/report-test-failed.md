### shelf: hello

**Failed** at commit `42a964eb0b4b`.
Not ready at the tests: test smoke failed with exit code 22: …
the app's own test smoke fails against this version; the previous version runs again. Its log says why.
Open it with `shelf app open hello`.

**Tests:**
- smoke failed with exit code 22
  <details><summary>end of its log</summary>

  ```
  curl: (22) The requested URL returned error: 500
  retrying would not help
  ```
  </details>

**Verification:** verified, signed by keyless, issuer https://token.actions.githubusercontent.com, subject ^https://github\.com/tweinmann/shelf/\.github/workflows/(build|release)\.yml@refs/(tags|heads)/.+$.

**Scan** (trivy 0.75.0, 2026-10-28T12:00:00Z):

| Component | Critical | High |
|---|---|---|
| web | 1 | 26 |

**Isolation:** Pod Security baseline; reaches its own components, DNS, the internet and no private addresses.

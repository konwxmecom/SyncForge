# Third-party notices

SyncForge uses third-party open-source packages. Those packages are **not** relicensed by the SyncForge MIT License; their respective copyright and license terms continue to apply. The versioned dependency inventory is in [`package-lock.json`](package-lock.json) and [`server/go.sum`](server/go.sum).

### JavaScript dependency licenses

The current npm dependency tree declares these SPDX license identifiers:

- **MIT** — includes TipTap, ProseMirror, Vite, and many transitive packages.
- **Apache-2.0** — includes TypeScript and `fake-indexeddb` (test dependency).
- **MPL-2.0** — includes Lightning CSS and its platform packages.
- **BSD-2-Clause** and **BSD-3-Clause** — used by individual transitive packages.
- **ISC** — used by individual transitive packages.

### Go dependencies

- [`github.com/gorilla/websocket`](https://github.com/gorilla/websocket) — BSD-2-Clause.

Review the exact package version's upstream `LICENSE`/`LICENSE.md` and metadata before redistributing binaries or source bundles. This summary is not a replacement for upstream notices; no blanket claim is made that every dependency uses MIT.

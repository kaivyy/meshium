# Phase 6C — Item Identity + Selection Semantics Contract (Part 1: Identity)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finalize the item-level identity + selection semantics contract for the canonical Meshium compare→select→apply→verify flow, so FE and BE speak one domain language and the honesty boundaries hold under selective apply.

**Architecture:** One canonical pipeline (`/migrations/new` → `/migrations/:id/pipeline`). Planner collects source category data at plan-time into `migration_steps.data`; compare derives the source side from those same persisted steps (never the onboarding discovery snapshot). Selective apply extends `initialSyncStage` — no new execution path. This document defines *identity*; Part 2 defines *semantics*; Part 3 defines *FE/BE contract*.

**Tech Stack:** Go backend (`internal/mod/migration`), SQLite (`migration_selections` table), Svelte 5 + `$lib/api/migrations.ts`.

## Global Constraints (frozen from spec §A + current repo)

- Canonical path: Wizard `/migrations/new`, Pipeline `/migrations/:id/pipeline`.
- Planner persists per-category collect into `migration_steps.data`; source-of-truth for apply = `migration_steps`, not `discovery_snapshots`.
- Selective apply extends `initialSyncStage`; honor `StepStatusApplied` guard; rollback only for what was truly applied.
- Honesty boundaries (never violated):
  - service exists ≠ runnable
  - config copied ≠ runtime ready
  - image present ≠ build context present
  - db discovered ≠ db data synced
  - skip ≠ success
  - keep_target ≠ same as source
  - applied ≠ verified
  - execute success ≠ app healthy
- Not every category must be item-level. Some are category-level or manual-placeholder only.

---

## C. Audit of item granularity from current data

Current collector shapes (verified in repo):

- `PackagesData{Packages []string, Distro, Count}`
- `ConfigsData{Files map[string][]byte}` (path→content)
- `ServicesData{Services []string}`
- `UsersData{Users []UserData{Name,UID,GID,HomeDir,Shell,Password}}` — groups NOT in collected shape
- `DockerData{Containers []DockerContainer{Name,Image,Status,Env,Labels}, Images []string, Volumes []DockerVolume{Name,Driver,Mountpoint}, ComposeFiles []DockerComposeFile{Path,Content}}`
- `DatabaseCollectData{Engine, Databases []DBCatalogEntry{Name,SizeMB}}`

Step granularity today: **one step per category** (`initialSyncStage` iterates categories). So 1 step = N items for every collectable category. Selective apply is therefore inherently *category-granular at the step level*, item-granular only at the selection layer — this is the central tension resolved in Part 2 §I/§J.

| Category | Current source shape | Smallest honest unit | Stable identity possible? | Recommended granularity | Risks / notes |
|---|---|---|---|---|---|
| packages | `[]string` names | one package name | Yes | **item-level** | Too granular: ordering — mitigate by sorting deterministically (already sorted by collector). Risk: version-pinned vs unpinned distro packages; identity = name only, not version. Acceptable: applying a named package is well-defined. |
| configs | `map[path]content` | one file path | Yes | **item-level** | Risky if directory-level config (e.g. `/etc/nginx/conf.d/*`): identity = exact path, so two files in a dir are distinct items; a renamed file looks like remove+add (no rename detection — acceptable, honest). Content is the compare value; path is the identity. |
| services | `[]string` unit names | one systemd unit | Yes | **item-level** | Identity = unit name. A service's enable/start is atomic per unit. Hard dep on its config + runtime (depmap). |
| users | `[]UserData{Name}` | one username | Yes | **item-level** (user only) | Groups are NOT collected → no `group:<name>` item yet. Home dir / shell are attributes, not identity. Password is collected but must never be a compare *value* (see §R). |
| docker images | `[]string` repo:tag | `repo:tag` | Yes (if tag pinned) | **item-level** | Identity weakness: floating tags (`latest`) are unstable across recompute. Decision: itemKey uses the tag as collected; if tag is `latest`/floating, mark `hardBlocked=false` but flag `unstableIdentity` warning. See §D unsupported cases. |
| docker volumes | `[]DockerVolume{Name}` | volume name | Yes | **item-level** | Data-bearing. Apply = create + copy data (guarded). Identity = volume name (unique per docker host). |
| docker containers | `[]DockerContainer{Name}` | container name | Yes | **item-level** | Recreated from image + def, not migrated live. Identity = container name. Dep on its image. |
| docker compose files | `[]DockerComposeFile{Path,Content}` | one compose file path | Yes | **item-level** (ADD in 6C) | **Currently NOT emitted as a parity item** — `compareDocker` only handles images/volumes/containers. Compose is a group-level definition: emitting `compose-file:<path>` is the honest unit; the project it defines groups its containers/images. |
| database engine presence | `DatabaseCollectData.Engine` | engine string | Yes | **category-level (derived)** | NOT a separate apply item today. `database-engine:<engine>` MAY be emitted as a *group/dependency anchor* item (applyLevel manual) but is not independently applied — the DB category step installs the engine as a prerequisite. |
| database data payload | `[]DBCatalogEntry{Name}` | `engine:dbName` | Yes | **item-level** | Identity `database:<engine>:<dbName>`. Real data transfer (dump/restore). Guarded; requires engine runtime present. This is the only category where "item applied" truly means "row data moved". |
| runtime deps (node, pm2, nginx, java, python…) | NOT collected as items; only implied via config/service deps | n/a | Partial | **manual-placeholder** | No collector emits a structured runtime inventory. Surfaced only as *dependency anchors* (depmap `engineRuntime`) if a service/config references them. No selectable item until a runtime collector exists. |
| app payload / application code | NOT collected | n/a | No | **manual-placeholder** | No code-collector. Must stay `manual_required` placeholder. |
| secret / env | collected inside container `Env` + user `Password` (raw!) | n/a | No (value unsafe) | **manual-placeholder** | Raw secrets exist in collected data but MUST NOT become item compare values or persisted action values. Placeholder only; handled by operator outside system. |
| cert | NOT collected | n/a | No | **manual-placeholder** | No cert collector. Placeholder. |
| DNS | NOT collected | n/a | No | **manual-placeholder** | No DNS collector. Placeholder. |
| unknown / hybrid | n/a | n/a | No | **manual-placeholder** | Anything depmap/collector cannot classify. |

---

## D. Final itemKey contract

### D.1 Global rules

1. **Deterministic** — pure function of `(category, stable attributes)`; no randomness, no timestamp.
2. **Stable across recompute** — same source/target state → same key. Recompute must produce identical keys or diffing selections breaks.
3. **Namespaced by category** — every key begins with `<category-namespace>:`.
4. **Collision-resistant** — namespace + identity attribute is unique per migration.
5. **Human-debuggable** — readable in logs/audit: `config:/etc/nginx/nginx.conf`.
6. **Not based on UI row order or array index** — keys survive sort/filter/reorder.
7. **Reusable** across compare / selection / verify / audit trail — one key, one lifecycle.

### D.2 Per-category convention (final)

| Category | itemKey format | Identity attribute | Notes |
|---|---|---|---|
| packages | `package:<name>` | package name | lowercase-normalized (distro package names are case-sensitive but convention-lower). No version in key. |
| configs | `config:<normalized-abs-path>` | absolute path, cleaned | `filepath.Clean`, leading `/` preserved. Symlinks resolved to realpath at collect time; key uses realpath. |
| services | `service:<unit-name>` | systemd unit name | exact unit name as `systemctl` knows it. |
| users | `user:<name>` | username | exact login name. |
| docker images | `docker-image:<repo>:<tag>` | repo + tag as collected | tag may be floating (`latest`) → `unstableIdentity` warning, still selectable. Digest preferred if available: `docker-image:<repo>@<digest>` when digest known. |
| docker volumes | `docker-volume:<name>` | volume name | docker host-unique. |
| docker containers | `docker-container:<name>` | container name | docker host-unique. |
| compose files | `compose-file:<normalized-abs-path>` | compose file path | NEW in 6C. Groups its containers/images via `parentKey`. |
| compose project | `compose-project:<project-name>` | project name (dir or `-p` flag) | Derived from compose file; group anchor. |
| database engine | `database-engine:<engine>` | engine string | Dependency anchor only; not independently applied. |
| database data | `database:<engine>:<dbName>` | engine + db name | The applied unit. |
| runtime | `runtime:<name>:<scope>` | name + version/scope if known | Emit ONLY as dependency anchor when referenced; not a selectable apply item until a runtime collector exists. |
| app | `app:<identifier-or-path>` | app id/path | Placeholder only in 6C. |
| secret-placeholder | `secret-placeholder:<scope>` | scope (e.g. `container:<name>` or `user:<name>`) | Placeholder only. |
| cert-placeholder | `cert-placeholder:<domain>` | domain | Placeholder only. |
| dns-placeholder | `dns-placeholder:<record-or-domain>` | record/domain | Placeholder only. |
| hybrid-placeholder | `hybrid-placeholder:<scope>` | scope | Anything unclassifiable. |

### D.3 Policy edge cases

- **Path normalization** — `filepath.Clean`; reject relative paths (must be absolute) → else treat as `unsupported` identity.
- **Case sensitivity** — keys are case-sensitive (Unix paths/usernames are). Do NOT downcase paths. Package names: downcase by distro convention only.
- **Duplicate names in different scopes** — namespace prevents collision (`user:nginx` vs `service:nginx` are distinct).
- **Multi-instance item** — e.g. two containers same image different names → distinct `docker-container:<name>` keys; fine.
- **Unnamed / partially discovered** — no name ⇒ `unsupported` identity, not selectable. Never invent a key.
- **Missing values** — if identity attribute empty ⇒ reject as item (`unsupported`).
- **Renamed resources** — no rename detection. A renamed config = `missing_on_target` (old key) + `missing_on_target`? No: old key `different`/gone, new key `missing_on_target`. Honest: looks like remove+add. Acceptable; do not synthesize rename link.
- **Ephemeral resources** — containers with random swarm names → still keyed by name; if name is ephemeral (regenerated each run) mark `unstableIdentity`. Not auto-applied by bulk-safe.
- **Unsupported identity cases** — see D.4.

### D.4 NOT selectable (identity too weak) — frozen list

- Any item with empty/relative identity attribute.
- `app:*`, `secret-placeholder:*`, `cert-placeholder:*`, `dns-placeholder:*`, `hybrid-placeholder:*` — placeholder only, `manual_required`.
- `runtime:*` emitted as dependency anchor only (not applied independently) until a runtime collector exists.
- `database-engine:*` — anchor only.
- Docker images with floating tag AND no digest → selectable but flagged `unstableIdentity` and excluded from `apply_safe` bulk (must be explicit).

---

## Item unit + granularity boundary (final answer)

| Category | Final item unit | Granularity | Executable at item level? |
|---|---|---|---|
| packages | `package:<name>` | item-level | Yes |
| configs | `config:<path>` | item-level | Yes |
| services | `service:<unit>` | item-level | Yes |
| users | `user:<name>` | item-level | Yes |
| docker images | `docker-image:<repo>:<tag>` | item-level | Yes (safe) |
| docker volumes | `docker-volume:<name>` | item-level | Yes (guarded) |
| docker containers | `docker-container:<name>` | item-level | Yes (guarded) |
| compose files | `compose-file:<path>` | item-level (group anchor) | Yes (guarded) — NEW |
| database engine | `database-engine:<engine>` | category-level anchor | No (prereq of DB step) |
| database data | `database:<engine>:<db>` | item-level | Yes (guarded, real data) |
| runtime deps | `runtime:<name>` | manual-placeholder anchor | No (placeholder) |
| app payload | `app:<id>` | manual-placeholder | No |
| secret / cert / dns | `*-placeholder:*` | manual-placeholder | No |

**Final unit + itemKey per category** (direct answer to §T):
- packages → `package:<name>`
- configs → `config:<abs-path>`
- services → `service:<unit>`
- users → `user:<name>`
- docker images → `docker-image:<repo>:<tag>`
- docker volumes → `docker-volume:<name>`
- docker containers → `docker-container:<name>`
- compose files → `compose-file:<abs-path>` (NEW)
- database → `database:<engine>:<dbName>`
- engine/runtime/app/secret/cert/dns → anchor or placeholder only, not independently applied.

Item-level executable: packages, configs, services, users, docker images, docker volumes, docker containers, compose files, database data.
Group/category-level only (not independently item-applied): database engine, runtime (anchor).
Manual-placeholder only: app, secret, cert, dns, hybrid, unknown runtime.

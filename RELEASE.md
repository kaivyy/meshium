# Release Checklist

This is the gate a Meshium release passes through before it is tagged and
published. It codifies what CI already enforces plus the manual verification CI
cannot do. Meshium follows [Semantic Versioning](https://semver.org/); the
version lives in `CHANGELOG.md` (the top `## [x.y.z]` heading) and the git tag.

## 1. Pre-flight (local, before opening the release PR)

- [ ] Working tree clean (`git status`) and on an up-to-date `main`.
- [ ] `go build ./...` succeeds.
- [ ] `go vet ./...` is clean.
- [ ] `go test ./... -count=1` passes.
- [ ] `go test -race ./... -count=1` passes (the race job in CI mirrors this).
- [ ] Frontend builds: `cd web && npm ci && npm run check && npm run build`.
- [ ] `make build` produces `bin/meshium` with the embedded frontend
      (`cmd/server/web/build/index.html` present).

## 2. Static analysis review (advisory, not blocking)

CI runs `staticcheck` and `govulncheck` as advisory jobs (see
`.github/workflows/ci.yml`). They do not fail the build, so a human reads them:

- [ ] Review the `staticcheck` output. New findings introduced by this release
      are fixed; pre-existing findings are triaged (see Known Limitations in the
      docs) rather than silently ignored.
- [ ] Review `govulncheck`. Any newly-disclosed CVE in a dependency that is
      actually reachable is addressed or explicitly accepted with a rationale.

## 3. Version & changelog

- [ ] `CHANGELOG.md` has a new `## [x.y.z] — YYYY-MM-DD` section describing the
      change set (Added / Changed / Fixed / Removed as applicable).
- [ ] The version number reflects the nature of the change (breaking → major,
      feature → minor, fix-only → patch).
- [ ] Claims in the changelog are grounded in the actual diff — no aspirational
      or descriptor-only capability is described as implemented.

## 4. Runtime verification

- [ ] Server starts on the fixed port (9527) and serves the embedded UI.
- [ ] Graceful shutdown works: SIGTERM drains and closes resources without
      leaking connections (see the lifecycle docs).
- [ ] Migrations interrupted mid-flight recover on restart (state-machine and
      pipeline recovery paths).
- [ ] Behind a reverse proxy, WebSocket-backed features (terminal, monitoring,
      log view, job/pipeline progress) connect — the origin check is
      proxy-aware.

## 5. Tag & publish

- [ ] Merge the release PR to `main`; confirm CI is green (test, race, frontend
      jobs required; lint job advisory).
- [ ] Tag the release: `git tag vX.Y.Z && git push origin vX.Y.Z`.
- [ ] Attach the built `bin/meshium` (and any platform variants) to the release.

## Future hardening (tracked, not yet in this checklist)

These are deliberately out of scope for the current release gate and are
tracked as follow-up work:

- **SBOM generation** — emit a CycloneDX/SPDX bill of materials per release.
- **Build provenance / signing** — SLSA provenance attestation and artifact
  signing (e.g. cosign) so downstream consumers can verify the binary.
- **Promote `staticcheck` to blocking** — once the pre-existing findings are
  cleared, flip the lint job from advisory to required.

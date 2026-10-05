---
name: dev-flow
description: End-to-end delivery flow for app-devper/pharmacy-api — from an idea or bug through shaping, design, test-first build, verify, review, PR into develop, release PR into main, tag, deploy check and back-merge. Says which skill to use at each stage. Use when starting any change, when unsure what comes next, or when asked to ship, release or hotfix.
---

# dev-flow — idea → release for pharmacy-api

One flow, nine stages. Each stage names the skill that does the work and the
exit check that must hold before moving on. Skip a stage only where it says
so. Branching and PR mechanics are owned by `git-flow` and `pr`; this skill
only sequences them.

> **Repo note:** Tests that need Mongo read `MONGO_TEST_URI` and skip without it. Changes to `main.go`, `config/`, `middleware/`, `routes/` or `scripts/identity-smoke/` also run the `identity-smoke` workflow (um-api + pharmacy-api together, ADR-0004) — it must be green too.

## 0. Pick the track

| Track | Starts at | Branch from → PR into |
|---|---|---|
| Feature / change | 1 | `develop` → `develop` (`feature/<slug>`) |
| Bug fix (not urgent) | 1 (short) | `develop` → `develop` (`feature/fix-<slug>`) |
| Refactor | 1 via `improve-codebase-architecture` | `develop` → `develop` |
| Release | 8 | `develop` → `main` (`release/X.Y.Z`) |
| Hotfix (production broken) | 9 | `main` → `main` (`hotfix/<slug>`) |

## 1. Shape — agree on what and why

- New behaviour or unclear requirement → `/grill-with-docs` (runs `grilling`
  + `domain-modeling`). The vocabulary is product-wide and lives in
  **pharmacy-app-kmp** (`CONTEXT-MAP.md` → `docs/contexts/<context>/CONTEXT.md`);
  this repo's `CONTEXT-MAP.md` only lists which of those contexts the API
  implements. So: a new or changed term → PR to pharmacy-app-kmp; a
  hard-to-reverse decision about this API → `docs/adr/` here; the API
  starting or stopping to own a context → this repo's `CONTEXT-MAP.md`.
- Small, well-understood fix → `grilling` only, a few questions, no docs.
- Refactor → `/improve-codebase-architecture` picks the candidate and grills it.

**Exit:** one or two sentences stating the change and how we will know it works.

## 2. Design — decide the shape of the code

- `codebase-design` for module/interface boundaries (deep modules, seams).
- Stack references: `golang-patterns`, `api-design` (new or changed endpoints), `redis-patterns`, `error-handling`.

**Exit:** files/modules to touch are known; any new term is in pharmacy-app-kmp's `docs/contexts/<context>/CONTEXT.md` (PR opened there).

## 3. Branch

`git-flow` → `feature/<slug>` from an up-to-date `develop`. One concern per
branch.

## 4. Build

Test first: `tdd-workflow` + `golang-testing` (table-driven tests next to the code). Write the failing test, make it pass, refactor.

Commit in small conventional commits: `feat(scope): …`, `fix(scope): …`,
`refactor(scope): …`, `test(scope): …`, `chore(scope): …`.

## 5. Verify locally — same gates as CI (`test`)

```bash
test -z "$(gofmt -l .)"   # fails if any file is unformatted
go build ./...
go vet ./...
go test ./...
```

Run the service locally (`go run .` with a `.env` from `.env.example` if present) and hit the changed endpoints.

**Exit:** everything above is green and the change was seen working — not
just compiled.

## 6. Review

`coding-standards`, `security-review` (auth, input, secrets, tenant isolation), then `/code-review`. Fix findings before opening the PR.

## 7. PR into develop

```bash
git push -u origin feature/<slug>
gh pr create --base develop --title "feat(scope): …" --body "…"
```

Body: what changed, why, how it was verified (stage 5). Land it with the
`pr` skill (squash, delete branch, sync `develop`). Never push to `develop`
directly.

## 8. Release — develop → main

1. **Pick the version** from what landed since the last tag
   (`git log $(git describe --tags --abbrev=0 --match 'v*' origin/main)..origin/develop --oneline`):
   breaking → major, any `feat` → minor, only `fix` → patch.
2. **Cut the branch:** `git-flow` release recipe → `release/X.Y.Z` from `develop`.
3. **Version:** No version file — the git tag is the version. The release PR needs no version commit.
4. **PR:** `gh pr create --base main --title "release: vX.Y.Z" --body "<changelog: PR list since last tag>"`.
   CI `test` must be green.
5. **Land:** `pr` skill (squash into `main`).
6. **Deploy:** Merging into `main` fires Cloud Build trigger `deploy-pharm-api` (project `devperpos`, region `global`; build config is inline on the trigger, not in this repo): buildpacks build → push image → `gcloud run services update pharmacy-api` (`asia-southeast1`). The pipeline does **not** run tests and does **not** set env vars — env lives on the Cloud Run service.
7. **Tag:** Tag by hand — nothing tags automatically.
   ```bash
   git checkout main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z" HEAD   # HEAD = the "release: vX.Y.Z (#n)" squash commit
   git push origin vX.Y.Z
   ```
8. **Back-merge** `main` → `develop` (`git-flow`; the `pr` skill does it
   after a PR into `main`).
9. **Check the deploy:**
   ```bash
   gcloud builds list --project=devperpos --region=global --limit=1 \
     --filter="substitutions.TRIGGER_NAME=deploy-pharm-api" --format="value(status,createTime,substitutions.SHORT_SHA)"
   gcloud run services describe pharmacy-api --project=devperpos --region=asia-southeast1 \
     --format="value(status.latestReadyRevisionName,status.url)"
   ```

**Exit:** tag `vX.Y.Z` on `main`, the build for that commit SUCCESS, `develop` contains `main`.

## 9. Hotfix — production is broken

`git-flow` hotfix recipe: `hotfix/<slug>` from `main` → stages 4–6 (test
reproducing the bug first) → PR into `main` titled `fix(scope): …` → release
steps 3 and 5–9 with a **patch** bump.

## Guard rails

- No direct pushes to `main` or `develop`; every change goes through a PR
  with `test` green.
- A merge into `main` is a release and a production deploy —
  only `release/*` and `hotfix/*` PRs target `main`.
- Never tag a commit that is not on `main`; never move or delete a pushed tag.

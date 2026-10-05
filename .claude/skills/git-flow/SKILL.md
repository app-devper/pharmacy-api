---
name: git-flow
description: Git flow branching for app-devper/pharmacy-api — main (production, deploys on merge), develop (default, integration), feature/*, release/*, hotfix/*. Use when starting new work, opening a PR, cutting a release, hotfixing production, or deciding which base branch a change targets.
---

# git-flow — branching + release model for pharmacy-api

The stage-by-stage flow lives in `dev-flow`; this skill owns the branch rules.

## Branch map

| Branch | Role | PR target | After merge |
|---|---|---|---|
| `main` | production — every merge deploys | — | back-merge into `develop` |
| `develop` | default branch, integration | — | — |
| `feature/<x>` | new work, fixes for next release | `develop` | delete branch |
| `release/<x.y.z>` | release cut from `develop` | `main` | tag, back-merge `main` → `develop`, delete branch |
| `hotfix/<x>` | urgent production fix from `main` | `main` | tag, back-merge `main` → `develop`, delete branch |

The `check` workflow (job `test`) runs on every PR and on pushes to
`main`/`develop`. PRs land via **squash**.

## What merging `main` does

Merging into `main` fires Cloud Build trigger `deploy-pharm-api` (project `devperpos`, region `global`; build config is inline on the trigger, not in this repo): buildpacks build → push image → `gcloud run services update pharmacy-api` (`asia-southeast1`). The pipeline does **not** run tests and does **not** set env vars — env lives on the Cloud Run service.

Version: No version file — the git tag is the version. The release PR needs no version commit.

Tag: Tag by hand — nothing tags automatically.

## Recipes

### Feature

```bash
git checkout develop && git pull --ff-only
git checkout -b feature/<slug>
# work, commit
git push -u origin feature/<slug>
gh pr create --base develop --title "feat(scope): ..." --body "..."
```

### Release

```bash
git checkout develop && git pull --ff-only
git checkout -b release/<x.y.z>
# version bump if this repo has a version file (see above)
git push -u origin release/<x.y.z>
gh pr create --base main --title "release: v<x.y.z>" --body "..."
```

### Hotfix

```bash
git checkout main && git pull --ff-only
git checkout -b hotfix/<slug>
# failing test, fix, (version bump if any)
git push -u origin hotfix/<slug>
gh pr create --base main --title "fix(scope): ..." --body "..."
```

### Tag (after a release/hotfix lands on main)

```bash
git checkout main && git pull --ff-only
git tag -a v<x.y.z> -m "v<x.y.z>" HEAD
git push origin v<x.y.z>
```

### Back-merge (required after every merge into main)

```bash
git checkout develop && git pull --ff-only
git merge main            # usually fast-forward; resolve if develop moved
git push origin develop
```

## Guard rails

- Never push directly to `main` or `develop` — always via PR.
- Never open a feature PR against `main`; only `release/*` and `hotfix/*`
  target `main`.
- Landing PRs (merge + branch cleanup + sync) is the **pr** skill's job.

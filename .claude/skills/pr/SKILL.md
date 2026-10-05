---
name: pr
description: Land the current branch's open pull request in app-devper/pharmacy-api — squash-merge (auto-merge if the check is still running), delete the branch, sync the base branch, and back-merge main into develop when the PR targeted main. Use when the user says "pr land", "merge it", "/pr", or otherwise asks to land the current PR.
---

# pr — land the current branch's PR

Feature PRs target `develop`; release/hotfix PRs target `main` (see
`git-flow`). The `check` workflow (job `test`) must be green. PRs land via
**squash**.

## Steps

1. **Find the PR for the current branch**
   ```bash
   gh pr view --json number,state,mergeable,mergeStateStatus,baseRefName,headRefName,title,url
   ```
   - No PR → stop and offer `gh pr create` with the base `git-flow` prescribes.
   - `MERGED`/`CLOSED` → report it; nothing to do.

2. **Push any unpushed commits**: `git push`

3. **Mergeability**: `CONFLICTING` → stop and report, do **not** force.
   `UNKNOWN` → re-run step 1 once.

4. **Squash-merge**
   ```bash
   gh pr checks <number>
   ```
   - Passed → `gh pr merge <number> --squash --delete-branch`
   - Running → `gh pr merge <number> --squash --auto --delete-branch`, then
     poll `gh pr view <number> --json state -q .state` until `MERGED`.
   - Failed → stop, show the failure, do not merge.

5. **Sync and clean up**
   ```bash
   git checkout <baseRefName> && git pull --ff-only origin <baseRefName>
   git branch -D <headRefName>
   git remote prune origin
   ```

6. **PR targeted `main`** → back-merge (mandatory):
   ```bash
   git checkout develop && git pull --ff-only origin develop
   git merge main && git push origin develop
   ```
   The merge into `main` has started the deploy — follow `dev-flow` stage 8 to tag and check it.

7. **Report**: PR number + URL, squash commit SHA on the base branch, branch
   deleted local + remote, and for `main` whether tag and deploy are done.

## Notes

- Default is `--squash`; use `--merge`/`--rebase` only when asked.
- Never merge over a red `check`.

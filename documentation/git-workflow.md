# Git Workflow

This repo has no CI/CD or branch protection configured, so the workflow below is manual — it relies on following these steps consistently rather than automated enforcement.

## Branch structure

- **`main`** — stable/release branch.
- **`develop`** — integration branch where features land first.

Never commit directly to `develop` or `main`. All work happens on short-lived feature branches merged in via pull request.

## Merge strategy

Historically, PRs into both `develop` and `main` were merged using **squash/rebase merge** (GitHub produces single-parent commits like `Develop (#76)`). Stick with this consistently for both directions (`feature → develop` and `develop → main`). Mixing merge strategies between the same two branches causes the local branch to silently diverge from its remote counterpart — different commit graphs even when the file content is identical — which then produces real merge conflicts and non-fast-forward pushes down the line.

## Adding a feature

1. Start the feature branch off `develop`, after pulling the latest:

   ```bash
   git checkout develop
   git pull origin develop
   git checkout -b feature/short-description
   ```

2. Work, commit normally.

3. Push and open a PR into `develop` (not `main`):

   ```bash
   git push -u origin feature/short-description
   gh pr create --base develop --title "..." --body "..."
   ```

4. Merge the PR using **"Squash and merge"** on GitHub.

5. Sync locally and delete the feature branch:

   ```bash
   git checkout develop
   git pull origin develop
   git branch -d feature/short-description
   git push origin --delete feature/short-description   # optional, tidy remote too
   ```

## Releasing `develop` into `main`

When `develop` is ready for a release, open a PR `develop → main`:

```bash
gh pr create --base main --head develop --title "Develop"
```

Merge it the same way (squash, consistently) so `main` and `develop` don't drift apart in commit-graph shape.

Afterward, sync both branches locally:

```bash
git checkout main    && git pull origin main
git checkout develop && git pull origin develop
```

## The one rule that prevents branch drift

Always `git pull origin develop` immediately before branching off it for new work. Letting a local branch sit unpulled while `origin/develop` moves on is exactly how a local branch can silently fall many commits behind, leading to real merge conflicts and rejected (non-fast-forward) pushes when you finally try to reconcile it.

# Git Workflow

This repo has no CI/CD or branch protection configured, so the workflow below is manual — it relies on following these steps consistently rather than automated enforcement.

## Branch structure

- **`main`** — stable/release branch.
- **`develop`** — integration branch where features land first.

Never commit directly to `develop` or `main`. All work happens on short-lived feature branches merged in via pull request.

## Merge strategy

Use **different strategies for the two directions**:

- **`feature → develop`: squash merge.** Feature branches are short-lived and disposable, so squashing them into one commit keeps `develop`'s log readable. Nothing else depends on a feature branch's internal history, so this is safe.
- **`develop → main`: a real merge commit (not squash, not rebase).** This is the one that matters: a merge commit makes `develop` a proper ancestor of `main`, so both branches share commit history and stay trivially in sync with a plain `git pull` afterward.

Squash-merging `develop → main` was the historical convention in this repo (commits like `Develop (#76)`, `Sync of the branches (#79)`), but **this is what causes branch drift, not what prevents it**: GitHub creates a brand-new, single-parent commit on `main` that `develop` never contains, so the two branches end up with identical file content but incompatible commit graphs — neither is an ancestor of the other. `git pull` can't fix that on its own. If you do keep squash-merging `develop → main` for any reason, you must follow it with the manual resync in the next section every single time.

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

Merge it on GitHub using **"Create a merge commit"** — not "Squash and merge" — so `main` ends up containing `develop`'s actual commits, not a copy of them.

Afterward, sync both branches locally. Use `--ff-only` rather than a plain `pull`: it makes git refuse and error out if a fast-forward isn't actually possible, instead of silently creating an unexpected merge commit.

```bash
git checkout main    && git pull --ff-only origin main
git checkout develop && git pull --ff-only origin develop
git pull --ff-only origin main      # bring the merge commit into develop too
git push origin develop             # publish that catch-up
```

If `--ff-only` ever fails here, stop — it means `develop` and `main` have actually diverged (e.g. someone squash-merged instead of using a merge commit), and you need the recovery steps below instead of forcing a merge.

### Do I need to run this after every merge to main?

No. Since `develop → main` uses a real merge commit, `develop` staying "behind" `main` by an unfetched merge commit is harmless and always safe to fast-forward through later — it's not drift. You can let a few releases pile up without syncing.

The one time it actually matters: **run it before branching off `develop` for new feature work**, so the feature doesn't miss anything that landed on `main` outside of `develop` (a hotfix, for example). Sanity-check first if you're unsure whether it's safe:

```bash
git fetch origin
git merge-base --is-ancestor origin/develop origin/main && echo "safe to fast-forward" || echo "diverged - do not pull, see recovery steps"
```

### If the PR was squash-merged anyway

Squash-merging `develop → main` leaves `develop` pointing at a commit `main` doesn't recognize as an ancestor, even though the content now matches. `git pull` will not resolve this — `develop` needs to be reset to match the new `main` tip:

```bash
git checkout develop
git fetch origin                # update the origin/main ref, without merging anything
git reset --hard origin/main
git push --force-with-lease origin develop
```

Only do this once you've confirmed `develop` has nothing in it that isn't already on `main` (`git diff origin/main develop` should be empty) — otherwise you'll discard real work.

## The one rule that prevents branch drift

Always pull immediately before branching off `develop` for new work — both from `develop` itself and from `main`, in case a release merge landed there since you last synced:

```bash
git checkout develop
git pull --ff-only origin develop
git pull --ff-only origin main
```

Letting a local branch sit unpulled while `origin/develop` moves on is exactly how a local branch can silently fall many commits behind, leading to real merge conflicts and rejected (non-fast-forward) pushes when you finally try to reconcile it.

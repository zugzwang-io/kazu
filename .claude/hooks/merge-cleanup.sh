#!/bin/bash
# Post-merge housekeeping: pull main forward, then remove local worktrees and
# branches already merged into origin/main (squash-merged branches are
# confirmed via gh before force-deleting). Fires after `gh pr merge` and at
# session start. Never touches dirty worktrees, unmerged branches, or main.
cd "$CLAUDE_PROJECT_DIR" || exit 0
git rev-parse --git-dir >/dev/null 2>&1 || exit 0

git fetch --prune --quiet 2>/dev/null || exit 0

if [ "$(git branch --show-current)" = "main" ]; then
  git pull --ff-only --quiet 2>/dev/null && echo "merge-cleanup: pulled main to $(git rev-parse --short HEAD)"
else
  git fetch origin main:main --quiet 2>/dev/null
fi

# Worktrees whose branch is fully merged; `remove` refuses dirty ones.
git worktree list --porcelain | awk '/^worktree /{wt=$2} /^branch /{print wt, $2}' |
while read -r wt ref; do
  b=${ref#refs/heads/}
  [ "$b" = "main" ] && continue
  if git merge-base --is-ancestor "refs/heads/$b" origin/main 2>/dev/null; then
    git worktree remove "$wt" 2>/dev/null && echo "merge-cleanup: removed worktree $wt ($b)"
  fi
done
git worktree prune 2>/dev/null

for b in $(git for-each-ref --format='%(refname:short)' refs/heads/); do
  [ "$b" = "main" ] && continue
  if git merge-base --is-ancestor "refs/heads/$b" origin/main 2>/dev/null; then
    git branch -d "$b" >/dev/null 2>&1 && echo "merge-cleanup: deleted branch $b"
  elif git for-each-ref --format='%(upstream:track)' "refs/heads/$b" | grep -q gone; then
    # Upstream deleted but not an ancestor: squash/rebase merge. Trust gh, not guesswork.
    if [ "$(gh pr view "$b" --json state --jq .state 2>/dev/null)" = "MERGED" ]; then
      git branch -D "$b" >/dev/null 2>&1 && echo "merge-cleanup: deleted squash-merged branch $b"
    fi
  fi
done
exit 0

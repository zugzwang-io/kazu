#!/bin/bash
# Post-merge housekeeping: pull main forward, then remove local worktrees and
# branches whose PR has merged (confirmed via gh, so squash merges count and a
# branch freshly cut from main doesn't). Fires after `gh pr merge` and at
# session start, which includes compaction. Never touches dirty worktrees,
# branches without a merged PR, or main.
cd "$CLAUDE_PROJECT_DIR" || exit 0
git rev-parse --git-dir >/dev/null 2>&1 || exit 0

git fetch --prune --quiet 2>/dev/null || exit 0

if [ "$(git branch --show-current)" = "main" ]; then
  git pull --ff-only --quiet 2>/dev/null && echo "merge-cleanup: pulled main to $(git rev-parse --short HEAD)"
else
  git fetch origin main:main --quiet 2>/dev/null
fi

# Merged means GitHub says the branch's PR merged and the local tip holds nothing
# beyond that PR's head. Ancestry of main isn't enough: a branch just cut from
# main, even one reusing a merged PR's name, is an ancestor of main too. No PR,
# or gh unreachable, means keep. Evidence branches are never deleted.
merged() {
  local b=$1 state head tip
  case $b in main | spike-network | spike-network-work | spike-noise) return 1 ;; esac
  read -r state head < <(gh pr view "$b" --json state,headRefOid --jq '.state + " " + .headRefOid' 2>/dev/null)
  [ "$state" = "MERGED" ] || return 1
  tip=$(git rev-parse "refs/heads/$b") || return 1
  [ "$tip" = "$head" ] || git merge-base --is-ancestor "$tip" "$head" 2>/dev/null
}

# `worktree remove` refuses dirty worktrees.
git worktree list --porcelain | awk '/^worktree /{wt=$2} /^branch /{print wt, $2}' |
while read -r wt ref; do
  b=${ref#refs/heads/}
  merged "$b" && git worktree remove "$wt" 2>/dev/null && echo "merge-cleanup: removed worktree $wt ($b)"
done
git worktree prune 2>/dev/null

for b in $(git for-each-ref --format='%(refname:short)' refs/heads/); do
  merged "$b" && git branch -D "$b" >/dev/null 2>&1 && echo "merge-cleanup: deleted merged branch $b"
done
exit 0

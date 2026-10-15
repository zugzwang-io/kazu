#!/bin/bash
# SessionStart hook: brief the model on live repo state so it acts proactively
# (failing CI -> offer /babysit; PR state drift -> /sync-plan) without being asked.
cd "$CLAUDE_PROJECT_DIR" || exit 0
command -v gh >/dev/null 2>&1 || exit 0

prs=$(gh pr list --state open --limit 20 \
  --json number,title,baseRefName,statusCheckRollup \
  --template '{{range .}}#{{.number}} {{.title}} [base:{{.baseRefName}}]{{$bad := 0}}{{$ok := 0}}{{range .statusCheckRollup}}{{if or (eq .conclusion "FAILURE") (eq .conclusion "TIMED_OUT") (eq .conclusion "CANCELLED")}}{{$bad = 1}}{{else}}{{$ok = 1}}{{end}}{{end}}{{if eq $bad 1}} CHECKS-FAILING{{else if eq $ok 1}} checks-green{{else}} no-checks(stacked){{end}}
{{end}}' 2>/dev/null) || exit 0

[ -z "$prs" ] && exit 0

phase=$(grep -m1 -E '^## Phase .*current focus' PLAN.md 2>/dev/null | sed 's/^## //')

echo "Repo brief (session start):"
echo "$prs"
[ -n "$phase" ] && echo "Plan: ${phase} — see PLAN.md"
if echo "$prs" | grep -q CHECKS-FAILING; then
  echo "Some checks are failing. Proactively offer (or run, if the user's request relates) /babysit."
fi
exit 0

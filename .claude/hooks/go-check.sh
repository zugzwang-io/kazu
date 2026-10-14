#!/bin/bash
# PostToolUse hook: after a .go file is edited, format it and surface the
# complaints CI's lint job would raise, so they're fixed in the same turn.
input=$(cat)
file=$(jq -r '.tool_input.file_path // empty' <<<"$input")
[[ "$file" == *.go ]] || exit 0
[ -f "$file" ] || exit 0

gofmt -w "$file"

dir=$(dirname "$file")
if command -v golangci-lint >/dev/null 2>&1; then
  out=$(cd "$dir" && golangci-lint run . 2>&1) || failed=1
else
  out=$(cd "$dir" && go vet . 2>&1) || failed=1
fi

if [ -n "$failed" ] && [ -n "$out" ]; then
  jq -n --arg c "Lint issues in $dir (fix before finishing):
$out" '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":$c}}'
fi
exit 0

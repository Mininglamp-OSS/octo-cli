#!/usr/bin/env bash
# Conventional Commits validator for the commit-msg hook (invoked by lefthook).
#
# Mirrors cc-channel-octo's loosened commitlint policy: enforce the
# `type(scope)?!?: subject` shape, but stay permissive about the rest so the
# repo's real style passes (uppercase technical scopes, long technical
# subjects, `!` breaking markers).
#
# Usage: bash .lefthook/commit-msg-check.sh <path-to-commit-msg-file>
set -euo pipefail

msg_file="${1:?commit message file path required}"

# First non-comment, non-blank line is the header.
header=$(grep -vE '^\s*#' "$msg_file" | grep -vE '^\s*$' | head -n1 || true)

# Skip machine-generated commits that don't follow the convention.
case "$header" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*|"Reapply "*)
    exit 0 ;;
esac

if [ -z "$header" ]; then
  echo "✖ commit message is empty." >&2
  exit 1
fi

types='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
# type, optional (scope) — scope is free-form, may contain spaces, optional ! then ": " then a non-empty subject.
pattern="^(${types})(\([^)]+\))?!?: .+"

if ! printf '%s' "$header" | grep -qE "$pattern"; then
  cat >&2 <<EOF
✖ Invalid commit message:

    $header

Expected Conventional Commits format:

    <type>(<scope>)?(!)?: <subject>

  type    one of: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert
  scope   optional, free-form (e.g. cli, thread, ci)
  !       optional, marks a breaking change
  subject short imperative description

Examples:
    feat(cli): add octo skills command
    fix(ci): pin golangci-lint-action to SHA
    refactor!: rename binary from octo to octo-cli

EOF
  exit 1
fi

# Soft length guard (loosened — repo has long technical subjects).
max=120
len=${#header}
if [ "$len" -gt "$max" ]; then
  echo "✖ Commit header is ${len} chars (max ${max}). Shorten the subject or move detail to the body." >&2
  exit 1
fi

exit 0

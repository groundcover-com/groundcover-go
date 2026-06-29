#!/usr/bin/env bash
#
# Fails if any commit in the current range was authored or committed by an
# identity that looks automated (an AI agent or bot). Human authorship is
# mandatory; see AGENTS.md.
set -euo pipefail

# Determine the commit range to inspect.
if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
  # Pull request: inspect commits not in the base branch.
  git fetch --no-tags --depth=100 origin "${GITHUB_BASE_REF}" >/dev/null 2>&1 || true
  range="origin/${GITHUB_BASE_REF}..HEAD"
else
  # Push / local: inspect the most recent commit only.
  range="HEAD~1..HEAD"
  if ! git rev-parse "HEAD~1" >/dev/null 2>&1; then
    range="HEAD"
  fi
fi

# Case-insensitive substrings that indicate a non-human identity. Do not ban
# GitHub noreply addresses: humans commonly use them, and GitHub web/squash
# merges use GitHub <noreply@github.com> as the committer.
banned_regex='(cursor|copilot|\[bot\]|-bot|bot@|claude|anthropic|gpt|openai|chatgpt|codex|devin|sweep|githubactions|github-actions)'

fail=0
while IFS=$'\t' read -r sha an ae cn ce; do
  for field in "$an" "$ae" "$cn" "$ce"; do
    if echo "$field" | grep -iEq "$banned_regex"; then
      echo "::error::Commit ${sha} has a non-human author/committer identity: '${field}'"
      fail=1
    fi
  done
done < <(git log --no-merges --format='%H%x09%an%x09%ae%x09%cn%x09%ce' "$range")

if [[ "$fail" -ne 0 ]]; then
  echo "AI/bot-authored commits are not allowed. See AGENTS.md."
  exit 1
fi

echo "All commits have human author/committer identities."

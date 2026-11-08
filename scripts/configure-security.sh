#!/usr/bin/env bash
# Apply the repository settings that cannot be activated by merging files.
# Run once as a repository administrator after reviewing docs/release-security.md.
set -euo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"
repo=vyrwu/atelier
gh api --method PUT "repos/$repo/vulnerability-alerts"
gh api --method PUT "repos/$repo/automated-security-fixes"
id="$(gh api --paginate "repos/$repo/rulesets" --jq '.[] | select(.name == "main: pull requests with green CI") | .id')"
if [[ -n "$id" ]]; then
  gh api --method PUT "repos/$repo/rulesets/$id" --input .github/security-ruleset.json
else
  gh api --method POST "repos/$repo/rulesets" --input .github/security-ruleset.json
fi

# The release App is the only actor that can create or change a release tag.
# Its credentials must subsequently be moved into the protected environments.
id="$(gh api --paginate "repos/$repo/rulesets" --jq '.[] | select(.name == "release tags: release App only") | .id')"
if [[ -n "$id" ]]; then
  gh api --method PUT "repos/$repo/rulesets/$id" --input .github/release-tag-ruleset.json
else
  gh api --method POST "repos/$repo/rulesets" --input .github/release-tag-ruleset.json
fi
gh api --method PUT "repos/$repo/actions/permissions/workflow" \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
for environment in release-automation release tap; do
  gh api --method PUT "repos/$repo/environments/$environment" \
    -F can_admins_bypass=false \
    -F 'deployment_branch_policy[protected_branches]=false' \
    -F 'deployment_branch_policy[custom_branch_policies]=true'
  if [[ "$environment" == release-automation ]]; then
    name=main type=branch
  else
    name='v*' type=tag
  fi
  # Replace branch policies so an earlier permissive selection cannot survive.
  policies="$(gh api --paginate "repos/$repo/environments/$environment/deployment-branch-policies" --jq '.branch_policies[].id')"
  while read -r policy_id; do
    [[ -n "$policy_id" ]] || continue
    gh api --method DELETE "repos/$repo/environments/$environment/deployment-branch-policies/$policy_id"
  done <<< "$policies"
  gh api --method POST "repos/$repo/environments/$environment/deployment-branch-policies" -f name="$name" -f type="$type"
done

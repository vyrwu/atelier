#!/usr/bin/env bash
# Refuse production keys unless the whole tag-to-main trust chain is protected.
set -euo pipefail
repo=vyrwu/atelier
cd "$(cd "$(dirname "$0")/../.." && pwd)"
fail() { echo "Signing protection check: $*" >&2; exit 1; }
# Refuse an accidentally copied script/checkout with a different origin.
origin="$(git config --get remote.origin.url)"
case "$origin" in
  https://github.com/vyrwu/atelier|https://github.com/vyrwu/atelier.git|git@github.com:vyrwu/atelier|git@github.com:vyrwu/atelier.git|ssh://github.com/vyrwu/atelier|ssh://github.com/vyrwu/atelier.git|ssh://git@github.com/vyrwu/atelier|ssh://git@github.com/vyrwu/atelier.git) ;;
  *) fail 'Run from a checkout whose origin is vyrwu/atelier on github.com.' ;;
esac
require() {
  local answer
  answer="$(gh api "$1" --jq "$2" --paginate)" || fail "Cannot inspect $1"
  # Every page must pass; never overlook a secret on a later page.
  [[ "${answer//$'\n'/}" =~ ^(true)+$ ]] || fail "$3"
}
for environment in release release-automation tap; do
  require "repos/$repo/environments/$environment" \
    '.can_admins_bypass == false and .deployment_branch_policy.custom_branch_policies == true' \
    "$environment must disallow admin bypass and use selected deployment refs."
  if [[ "$environment" == release-automation ]]; then expected='["branch:main"]'; else expected='["tag:v*"]'; fi
  require "repos/$repo/environments/$environment/deployment-branch-policies" \
    "[.branch_policies[] | \"\(.type):\(.name)\"] == $expected" \
    "$environment has unexpected deployment refs."
done
require "repos/$repo/actions/permissions/workflow" \
  '.can_approve_pull_request_reviews == false and .default_workflow_permissions == "read"' \
  'Workflow tokens must default to read-only and cannot approve PRs.'
require "repos/$repo/actions/secrets" \
  'all(.secrets[]; (.name | test("^(RELEASE_APP_PRIVATE_KEY|HOMEBREW_TAP_APP_PRIVATE_KEY|ATELIER_SIGNING_.*)$")) | not)' \
  'Move App/signing private keys out of repository secrets into the protected environments.'
# This is a personal repository, so there can be no inherited organization secret
# exposing the tag-creating App to branch workflows.
require "repos/$repo" '.owner.type == "User"' 'Re-audit inherited organization secrets before using this in an organization.'
require "repos/$repo/environments/release-automation/secrets" \
  'any(.secrets[]; .name == "RELEASE_APP_PRIVATE_KEY")' \
  'The release-automation environment needs RELEASE_APP_PRIVATE_KEY.'
for name in 'main: pull requests with green CI' 'release tags: release App only'; do
  ruleset_id="$(gh api --paginate "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")"
  [[ "$ruleset_id" =~ ^[0-9]+$ ]] || fail "Missing or ambiguous ruleset: $name"
  if [[ "$name" == main:* ]]; then
    # shellcheck disable=SC2016 # $checks and $context are jq variables.
    require "repos/$repo/rulesets/$ruleset_id" \
      '.enforcement == "active" and .target == "branch" and
       .conditions.ref_name.include == ["refs/heads/main"] and .conditions.ref_name.exclude == [] and
       ((.bypass_actors // []) | length) == 0 and
       any(.rules[]; .type == "pull_request" and .parameters.required_approving_review_count >= 1 and .parameters.require_last_push_approval and .parameters.dismiss_stale_reviews_on_push and .parameters.required_review_thread_resolution) and
       any(.rules[]; .type == "required_status_checks" and .parameters.strict_required_status_checks_policy and
         (.parameters.required_status_checks as $checks |
          all(["lint", "goreleaser-check", "build-and-test (ubuntu-latest)", "build-and-test (macos-latest)"][];
            . as $context | any($checks[]; .context == $context and .integration_id == 15368)))) and
       any(.rules[]; .type == "deletion") and any(.rules[]; .type == "non_fast_forward")' \
      'Main must require reviewed, up-to-date PRs with no bypass, force pushes or deletion.'
  else
    require "repos/$repo/rulesets/$ruleset_id" \
      '.enforcement == "active" and .target == "tag" and
       .conditions.ref_name.include == ["refs/tags/v*"] and .conditions.ref_name.exclude == [] and
       .bypass_actors == [{"actor_id":4281327,"actor_type":"Integration","bypass_mode":"always"}] and
       any(.rules[]; .type == "creation") and any(.rules[]; .type == "update") and any(.rules[]; .type == "deletion")' \
      'Only the release App may create, update or delete v* tags.'
  fi
done
# Tag protections do not retroactively make old, off-main tags trustworthy.
git fetch origin main --tags
while read -r tag; do
  git merge-base --is-ancestor "refs/tags/$tag^{commit}" origin/main ||
    fail "Existing tag $tag is outside main; remove or audit it before provisioning."
done < <(git tag --list 'v*')
echo 'Release environments, main reviews and release-tag protections are in place.'

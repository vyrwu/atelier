#!/usr/bin/env bash
# Exercise real setup with a fake GitHub API that has paginated ref policies.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/security-setup.XXXXXX")"
trap 'rm -rf "$work"' EXIT
export SECURITY_SETUP_TEST_STATE="$work/state.json"
cat > "$work/gh" <<'PY'
#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path

args = sys.argv[1:]
assert args.pop(0) == "api", args
state_file = Path(os.environ["SECURITY_SETUP_TEST_STATE"])
state = json.loads(state_file.read_text()) if state_file.exists() else {
    env: [11, 22, 33] for env in ["release-automation", "release", "tap"]
}
endpoint = next(a for a in args if a.startswith("repos/"))
method = args[args.index("--method") + 1] if "--method" in args else "GET"
if endpoint.endswith("/rulesets"):
    assert method == "GET", args
    print(1)
elif "/deployment-branch-policies" in endpoint:
    environment = endpoint.split("/environments/")[1].split("/")[0]
    if method == "GET":
        # A first-page-only client misses policies 22/33, including a wildcard.
        ids = state[environment] if "--paginate" in args else state[environment][:1]
        print("\n".join(str(i) for i in ids))
    elif method == "DELETE":
        state[environment].remove(int(endpoint.rsplit("/", 1)[1]))
    elif method == "POST":
        fields = dict(a.split("=", 1) for a in args if a.startswith(("name=", "type=")))
        state[environment].append(fields)
    else:
        raise AssertionError(args)
else:
    assert method == "PUT", args
state_file.write_text(json.dumps(state))
PY
chmod +x "$work/gh"
PATH="$work:$PATH" "$root/scripts/configure-security.sh"
python3 - <<'PY'
import json
import os
from pathlib import Path
state = json.loads(Path(os.environ["SECURITY_SETUP_TEST_STATE"]).read_text())
for environment, policies in state.items():
    expected = {"name": "main", "type": "branch"} if environment == "release-automation" else {"name": "v*", "type": "tag"}
    assert policies == [expected], f"{environment} retained permissive policies: {policies}"
print("Security setup removes every policy page before allowing the intended deployment ref.")
PY

#!/usr/bin/env bash
# Build a throwaway Odoo lab from a public image pinned by digest, run every conformance pass against
# it, and compare the result with the series' manifest (odoo-conformance-gate.py).
#
# The same script runs on a developer machine (docker or podman) and in the public repository's
# conformance workflow, so what CI proves is exactly what anyone can reproduce:
#
#   odoo-conformance-lab.sh --series 20.0 --out /tmp/conf-20 [--port 58740] [--engine podman] [--keep]
#
# --setup-only builds the lab and keeps it, without running a pass: for one runner's --only.
#
# What it does, in order:
#   1. a private network, a Postgres container and an Odoo database created --with-demo with every
#      module some catalog operation requires (odoo-conformance-labs.json);
#   2. the Odoo web server on 127.0.0.1:<port>, and odoo-conformance-lab-setup.py inside it (accounting
#      group, Demo payment provider, a record in the models demo data leaves empty, an API key);
#   3. the read pass, the lab seeder, the read pass again, the write pass, the action pass, the
#      curated money flows, the recurring-task workflow, the Settings page and the controls workflow,
#      each recorded by the runners as evidence;
#   4. the gate: <out>/summary.md and <out>/gate.json, exit 1 when a verified operation regressed.
#
# Nothing it writes holds a secret except <out>/secret (mode 700), which is never uploaded: the
# database password is random, the API key is the lab administrator's and both die with the lab.
set -euo pipefail

SERIES="" OUT="" PORT="" ENGINE="" KEEP=false ODOO_BIN="" SETUP_ONLY=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --series) SERIES="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --engine) ENGINE="$2"; shift 2 ;;
    --odoo-bin) ODOO_BIN="$2"; shift 2 ;;
    --keep) KEEP=true; shift ;;
    --setup-only) SETUP_ONLY=true; KEEP=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$SERIES" ] && [ -n "$OUT" ] || { echo "usage: $0 --series 19.0|20.0 --out DIR [--port N] [--engine docker|podman] [--odoo-bin PATH] [--keep] [--setup-only]" >&2; exit 2; }
ENGINE="${ENGINE:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}"
PORT="${PORT:-$([ "$SERIES" = "19.0" ] && echo 58739 || echo 58740)}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The monorepo keeps the runners in scripts/dev and the catalog in packages/odoo; the public
# repository keeps them in conformance/ and third_party/packages/odoo.
if [ -f "$HERE/../../packages/odoo/odoo19_conformance_manifest.json" ]; then
  ROOT="$(cd "$HERE/../.." && pwd)"; CORE="$ROOT/packages/odoo"; RESEARCH="$ROOT/docs/architecture/provider-research/odoo-surface"
  BUILD_DIR="$ROOT/packages/cli"; BUILD_PKG="./cmd/odoo-cli"
else
  ROOT="$(cd "$HERE/.." && pwd)"; CORE="$ROOT/third_party/packages/odoo"; RESEARCH="$ROOT/docs/odoo-surface"
  BUILD_DIR="$ROOT"; BUILD_PKG="./cmd/odoo-cli"
fi
MANIFEST="$CORE/odoo${SERIES%%.*}_conformance_manifest.json"
LABS="$HERE/odoo-conformance-labs.json"
lab() { python3 -c "import json,sys; d=json.load(open('$LABS')); s=d['series']['$SERIES']; print($1)"; }
IMAGE="$(lab "s['image']")"
MODULES="$(lab "','.join(s['modules'])")"
POSTGRES="$(python3 -c "import json; print(json.load(open('$LABS'))['postgres'])")"
RENAMES="$(lab "s.get('renames','')")"

mkdir -p "$OUT/evidence" "$OUT/secret" "$OUT/xdg/aerp" && chmod 700 "$OUT/secret"
NAME="odoo-conformance-${SERIES%%.*}"
NET="$NAME-net"
cleanup() {
  if [ "$KEEP" != true ]; then
    "$ENGINE" rm -f "$NAME-web" "$NAME-db" >/dev/null 2>&1 || true
    "$ENGINE" network rm "$NET" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
cleanup

PW="$(python3 -c 'import secrets; print(secrets.token_urlsafe(24))')"
printf '%s' "$PW" > "$OUT/secret/db-password"
"$ENGINE" network create "$NET" >/dev/null
"$ENGINE" run -d --name "$NAME-db" --network "$NET" -e POSTGRES_USER=odoo -e POSTGRES_PASSWORD="$PW" -e POSTGRES_DB=postgres "$POSTGRES" >/dev/null
for _ in $(seq 1 60); do "$ENGINE" exec "$NAME-db" pg_isready -U odoo >/dev/null 2>&1 && break; sleep 2; done

echo "[$(date +%T)] $SERIES: creating the database with demo data (${MODULES//,/ })"
"$ENGINE" run --rm --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" "$IMAGE" \
  odoo -d conformance -i "$MODULES" --with-demo --stop-after-init --log-level=warn > "$OUT/init.log" 2>&1
"$ENGINE" run -d --name "$NAME-web" --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" \
  -p "127.0.0.1:$PORT:8069" "$IMAGE" odoo -d conformance --db-filter=^conformance$ --http-interface=0.0.0.0 >/dev/null
# Ready means the web server answers a page AND a JSON-RPC call; a port that accepts connections is not.
wait_ready() {
  for _ in $(seq 1 120); do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/web/login")" = 200 ] &&
       curl -s -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"call","params":{"service":"common","method":"version","args":[]}}' \
         "http://127.0.0.1:$PORT/jsonrpc" | grep -q server_serie; then
      return 0
    fi
    sleep 5
  done
  echo "the Odoo web server did not become ready in 10 minutes" >&2
  "$ENGINE" logs --tail 40 "$NAME-web" >&2 || true
  exit 1
}
wait_ready

echo "[$(date +%T)] $SERIES: preparing the lab"
"$ENGINE" exec -i "$NAME-web" sh -c 'odoo shell -c /etc/odoo/odoo.conf --db_host="$HOST" --db_user="$USER" --db_password="$PASSWORD" -d conformance --no-http --log-level=error' \
  < "$HERE/odoo-conformance-lab-setup.py" > "$OUT/secret/setup.out" 2> "$OUT/setup.log"
grep '^SETUP ' "$OUT/secret/setup.out" || true
sed -n 's/^ASAO_KEY=//p' "$OUT/secret/setup.out" > "$OUT/secret/api-key"
[ -s "$OUT/secret/api-key" ] || { echo "the lab gave no API key; see $OUT/setup.log" >&2; exit 1; }

wait_ready
if [ -z "$ODOO_BIN" ]; then
  ODOO_BIN="$OUT/odoo-cli"
  (cd "$BUILD_DIR" && go build -o "$ODOO_BIN" "$BUILD_PKG")
fi
cat > "$OUT/xdg/aerp/profiles.json" <<EOF
{"profiles": {"conformance": {"endpoint": "http://127.0.0.1:$PORT", "database": "conformance", "login": "admin", "credentialRef": "conformance", "protocol": "json2"}}}
EOF
export XDG_CONFIG_HOME="$OUT/xdg" ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true ODOO_ORACLE_PASSWORD=admin PYTHONDONTWRITEBYTECODE=1
AERP_CREDENTIAL_CONFORMANCE="$(cat "$OUT/secret/api-key")"
export AERP_CREDENTIAL_CONFORMANCE
DIGEST="${IMAGE##*@}"
COMMON=(--odoo-bin "$ODOO_BIN" --profile conformance --endpoint "http://127.0.0.1:$PORT" --database conformance --login admin
        --series "$SERIES" --image-digest "$DIGEST")
RENAME_ARGS=()
[ -n "$RENAMES" ] && RENAME_ARGS=(--renames "$RESEARCH/$RENAMES")
if [ "$SETUP_ONLY" = true ]; then
  # A lab to run one runner against (--only): built, set up and kept, no pass run.
  echo "[$(date +%T)] $SERIES: lab ready on 127.0.0.1:$PORT (profile conformance, XDG_CONFIG_HOME=$OUT/xdg, key in $OUT/secret/api-key)"
  exit 0
fi
INVENTORY="$OUT/inventory-$SERIES.json"
gzip -dc "$RESEARCH/inventory-$SERIES.json.gz" > "$INVENTORY"
E="$OUT/evidence"
CURATED=crm.opportunity.convert,crm.opportunity.lose,crm.opportunity.win,finance.invoice.cancel,finance.invoice.draft,finance.invoice.post,finance.payment.register,inventory.transfer.cancel,inventory.transfer.reserve,inventory.transfer.validate,people.employee.offboard,procurement.order.cancel,procurement.order.confirm,procurement.order.draft,sales.order.cancel,sales.order.confirm,sales.order.draft,sales.invoice.create,work.milestone.reach,procurement.bill.create

pass() { echo "[$(date +%T)] $SERIES: $1"; shift; python3 -I "$@" 2>> "$OUT/runs.log"; tail -1 "$OUT/runs.log"; }
pass "reads" "$HERE/odoo-read-conformance.py" "${COMMON[@]}" "${RENAME_ARGS[@]}" --readiness ANY --evidence "$E/read-1.json"
echo "[$(date +%T)] $SERIES: seeding the models the reads found empty"
XDG_CONFIG_HOME="$OUT/empty-xdg" python3 -I "$HERE/odoo-lab-seed.py" --odoo-bin "$ODOO_BIN" --endpoint "http://127.0.0.1:$PORT" \
  --database conformance --login admin --from-evidence "$E/read-1.json" > "$OUT/seed.log" 2>&1 || true
pass "reads after seeding" "$HERE/odoo-read-conformance.py" "${COMMON[@]}" "${RENAME_ARGS[@]}" --readiness ANY --evidence "$E/read-2.json"
pass "writes" "$HERE/odoo-write-conformance.py" "${COMMON[@]}" "${RENAME_ARGS[@]}" --readiness ANY --evidence "$E/write.json"
pass "actions and dialogs" "$HERE/odoo-action-conformance.py" "${COMMON[@]}" --inventory "$INVENTORY" --readiness ANY --evidence "$E/action.json"
pass "curated money flows" "$HERE/odoo-action-conformance.py" "${COMMON[@]}" --inventory "$INVENTORY" --readiness ANY --only "$CURATED" --evidence "$E/money-flows.json"
pass "recurring tasks" "$HERE/odoo-task-recurrence-conformance.py" "${COMMON[@]}" --evidence "$E/task-recurrence.json"
# The settings oracle signs in with the API key: a run that requires two-step sign-in of employees
# would lock a password out of RPC (odoo-settings-conformance.py).
pass "settings" "$HERE/odoo-settings-conformance.py" "${COMMON[@]}" --password-env AERP_CREDENTIAL_CONFORMANCE --evidence "$E/settings.json"
# The controls workflow last: it turns controls on, exercises what Odoo then does and restores them.
# Its records are controls, not operations: the gate does not read them, and a run that stops there
# does not stop the comparison. Its outcome joins the summary below.
pass "controls" "$HERE/odoo-controls-conformance.py" "${COMMON[@]}" --password-env AERP_CREDENTIAL_CONFORMANCE --evidence "$E/controls.json" || true
# The alerts: their operations' records are compared like any other; what Odoo then does joins the summary.
pass "alerts" "$HERE/odoo-alerts-conformance.py" "${COMMON[@]}" --password-env AERP_CREDENTIAL_CONFORMANCE --evidence "$E/alerts.json"

EVIDENCE=("$E/read-1.json" "$E/read-2.json" "$E/write.json" "$E/action.json" "$E/money-flows.json" "$E/task-recurrence.json"
          "$E/settings.json" "$E/alerts.json")
echo "[$(date +%T)] $SERIES: comparing with the manifest"
if ! python3 "$HERE/odoo-conformance-gate.py" --series "$SERIES" --manifest "$MANIFEST" --evidence "${EVIDENCE[@]}" \
       --summary "$OUT/summary.md" --report "$OUT/gate.json"; then
  # A disagreement is a regression only when it repeats: an action whose result depends on the order
  # its twins ran in, or on the clock, is sampled once more before the gate decides.
  RECHECK="$(python3 -c "import json; print(','.join(i['operation'] for i in json.load(open('$OUT/gate.json'))['regressed']))")"
  echo "[$(date +%T)] $SERIES: sampling the disagreements once more: $RECHECK"
  python3 -I "$HERE/odoo-action-conformance.py" "${COMMON[@]}" --inventory "$INVENTORY" --readiness ANY --only "$RECHECK" \
    --evidence "$E/recheck.json" 2>> "$OUT/runs.log" || true
  [ -s "$E/recheck.json" ] && EVIDENCE+=("$E/recheck.json")
  python3 "$HERE/odoo-conformance-gate.py" --series "$SERIES" --manifest "$MANIFEST" --evidence "${EVIDENCE[@]}" \
    --summary "$OUT/summary.md" --report "$OUT/gate.json"
fi
# The controls workflow's outcome, after the operations' comparison.
python3 - "$E/controls.json" "$OUT/summary.md" <<'PY' || true
import json, sys
path, summary = sys.argv[1], sys.argv[2]
lines = ["", "## Controls (odoo-cli controls)", ""]
try:
    for control, result in sorted(json.load(open(path, encoding="utf-8"))["records"].items()):
        failures = "; ".join(result.get("failures") or [])
        lines.append(f"- {control}: {result['status']}" + (f" ({failures})" if failures else ""))
except (OSError, ValueError, KeyError):
    lines.append("The controls workflow did not finish; see runs.log.")
try:
    alerts = json.load(open(path.replace("controls.json", "alerts.json"), encoding="utf-8"))["alerts"]
    for control, result in sorted(alerts.items()):
        failures = "; ".join(result.get("failures") or [])
        lines.append(f"- {control} (alert): {result['status']}" + (f" ({failures})" if failures else ""))
except (OSError, ValueError, KeyError):
    lines.append("The alerts workflow did not finish; see runs.log.")
with open(summary, "a", encoding="utf-8") as out:
    out.write("\n".join(lines) + "\n")
PY

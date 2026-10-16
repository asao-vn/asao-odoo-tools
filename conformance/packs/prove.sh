#!/usr/bin/env bash
# prove.sh --out DIR [--engine docker|podman] [--odoo-bin PATH] [--keep]
#
# The live proof of operation packs (DEC-2026-0176). It builds a throwaway Odoo 19.0 from the image
# the conformance lab pins, with only base and salon_demo (a custom module of this folder that ships a
# pack at static/odoo-cli/pack.json), then runs prove.py: pack generate, pack sync, pack show, the MCP
# tools, and every operation of the pack through odoo-cli, each checked against Odoo by a JSON-RPC
# client of its own. The lab is removed at the end unless --keep.
set -euo pipefail

OUT=""; ENGINE=""; BIN=""; KEEP=false
while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --engine) ENGINE="$2"; shift 2 ;;
    --odoo-bin) BIN="$2"; shift 2 ;;
    --keep) KEEP=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$OUT" ] || { echo "usage: $0 --out DIR [--engine docker|podman] [--odoo-bin PATH] [--keep]" >&2; exit 2; }
ENGINE="${ENGINE:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LABS="$HERE/../odoo-conformance-labs.json"
IMAGE="$(python3 -c "import json; print(json.load(open('$LABS'))['series']['19.0']['image'])")"
POSTGRES="$(python3 -c "import json; print(json.load(open('$LABS'))['postgres'])")"
NAME=odoo-pack-proof
NET="$NAME-net"
PORT=58745
mkdir -p "$OUT/secret" "$OUT/xdg/aerp" && chmod 700 "$OUT/secret"
OUT="$(cd "$OUT" && pwd)"

gone() {
  "$ENGINE" rm -f -v "$NAME-web" "$NAME-db" >/dev/null 2>&1 || true
  "$ENGINE" network rm "$NET" >/dev/null 2>&1 || true
}
gone
trap '[ "$KEEP" = true ] || gone' EXIT

if [ -z "$BIN" ]; then
  BIN="$OUT/odoo-cli"
  if [ -f "$HERE/../../../packages/cli/cmd/odoo-cli/main.go" ]; then
    (cd "$HERE/../../../packages/cli" && go build -o "$BIN" ./cmd/odoo-cli)
  else
    (cd "$HERE/../.." && go build -o "$BIN" ./cmd/odoo-cli)
  fi
fi

# Hex: the Odoo image hands the password to wait-for-psql.py as an argument of its own.
PW="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
"$ENGINE" network create "$NET" >/dev/null
"$ENGINE" run -d --name "$NAME-db" --network "$NET" -e POSTGRES_USER=odoo -e POSTGRES_PASSWORD="$PW" -e POSTGRES_DB=postgres "$POSTGRES" >/dev/null
for _ in $(seq 1 60); do "$ENGINE" exec "$NAME-db" pg_isready -U odoo >/dev/null 2>&1 && break; sleep 2; done

ADDONS=(-v "$HERE/salon_demo:/mnt/extra-addons/salon_demo:ro")
echo "[$(date +%T)] creating the database with salon_demo"
"$ENGINE" run --rm --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" "${ADDONS[@]}" "$IMAGE" \
  odoo -d proof -i salon_demo --stop-after-init --log-level=warn > "$OUT/init.log" 2>&1 ||
  { tail -40 "$OUT/init.log" >&2; exit 1; }
"$ENGINE" run -d --name "$NAME-web" --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" "${ADDONS[@]}" \
  -p "127.0.0.1:$PORT:8069" "$IMAGE" odoo -d proof --db-filter=^proof$ --http-interface=0.0.0.0 >/dev/null
for _ in $(seq 1 120); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/web/login")" = 200 ] &&
     curl -s -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"call","params":{"service":"common","method":"version","args":[]}}' \
       "http://127.0.0.1:$PORT/jsonrpc" | grep -q server_serie; then
    break
  fi
  sleep 5
done

echo "[$(date +%T)] the administrator's API key"
"$ENGINE" exec -i "$NAME-web" sh -c 'odoo shell -c /etc/odoo/odoo.conf --db_host="$HOST" --db_user="$USER" --db_password="$PASSWORD" -d proof --no-http --log-level=error' \
  > "$OUT/secret/key.out" 2> "$OUT/key.log" <<'PY'
import datetime
from odoo import release
admin = env.ref("base.user_admin")
scope = "rpc" if release.version_info[0] >= 20 else None
key = env["res.users.apikeys"].with_user(admin)._generate(scope, "odoo-pack-proof", datetime.datetime.now() + datetime.timedelta(days=1))
env.cr.commit()
print("PROOF_KEY=" + key)
PY
sed -n 's/^PROOF_KEY=//p' "$OUT/secret/key.out" > "$OUT/secret/api-key"
[ -s "$OUT/secret/api-key" ] || { echo "the lab gave no API key; see $OUT/key.log" >&2; exit 1; }

cat > "$OUT/xdg/aerp/profiles.json" <<EOF
{"profiles": {"proof": {"endpoint": "http://127.0.0.1:$PORT", "database": "proof", "login": "admin", "credentialRef": "proof", "protocol": "json2"}}}
EOF
export XDG_CONFIG_HOME="$OUT/xdg" ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true ODOO_ORACLE_PASSWORD=admin
AERP_CREDENTIAL_PROOF="$(cat "$OUT/secret/api-key")"
# The report is served to a logged-in web session only.
AERP_CREDENTIAL_PROOF_WEB=admin
export AERP_CREDENTIAL_PROOF AERP_CREDENTIAL_PROOF_WEB
echo "[$(date +%T)] the pack, through odoo-cli"
python3 -I "$HERE/prove.py" --odoo-bin "$BIN" --profile proof --endpoint "http://127.0.0.1:$PORT" --database proof --login admin --out "$OUT"

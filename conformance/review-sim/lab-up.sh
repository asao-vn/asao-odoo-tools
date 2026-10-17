#!/usr/bin/env bash
# A throwaway Odoo 19 for the review simulation: an empty database (no Odoo demo data) with the
# Vietnamese chart of accounts, in VND, holding only the business simulate.py writes.
#
#   lab-up.sh --out DIR [--port 58741] [--engine podman|docker]
#
# DIR/secret (mode 700) holds the random database password and the lab administrator's API key; both
# die with the lab. DIR/xdg holds an odoo-cli profile "review-sim" that reads the key from
# AERP_CREDENTIAL_REVIEW_SIM. lab-down.sh removes the containers and the network.
set -euo pipefail

OUT="" PORT=58741 ENGINE=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --engine) ENGINE="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$OUT" ] || { echo "usage: $0 --out DIR [--port N] [--engine podman|docker]" >&2; exit 2; }
ENGINE="${ENGINE:-$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LABS="$HERE/../odoo-conformance-labs.json"
IMAGE="$(python3 -c "import json; print(json.load(open('$LABS'))['series']['19.0']['image'])")"
POSTGRES="${REVIEW_SIM_POSTGRES:-docker.io/library/postgres:16-alpine}"
MODULES="l10n_vn,sale_management,purchase,stock,hr_expense,hr_holidays,point_of_sale"
NAME="odoo-review-sim" NET="odoo-review-sim-net" DB="reviewsim"

mkdir -p "$OUT/secret" "$OUT/xdg/aerp" && chmod 700 "$OUT/secret"
"$ENGINE" rm -f "$NAME-web" "$NAME-db" >/dev/null 2>&1 || true
"$ENGINE" network rm "$NET" >/dev/null 2>&1 || true

# Hex: the Odoo image hands the password to wait-for-psql.py as an argument of its own, and argparse
# takes one that starts with "-" for an option.
PW="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
printf '%s' "$PW" > "$OUT/secret/db-password"
"$ENGINE" network create "$NET" >/dev/null
"$ENGINE" run -d --name "$NAME-db" --network "$NET" -e POSTGRES_USER=odoo -e POSTGRES_PASSWORD="$PW" -e POSTGRES_DB=postgres "$POSTGRES" >/dev/null
for _ in $(seq 1 60); do "$ENGINE" exec "$NAME-db" pg_isready -U odoo >/dev/null 2>&1 && break; sleep 2; done

# The company must be in Vietnam before accounting installs, or Odoo loads its generic chart.
echo "[$(date +%T)] creating $DB without demo data (${MODULES//,/ })"
odoo_run() { "$ENGINE" run --rm -i --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" "$IMAGE" "$@"; }
odoo_run odoo -d "$DB" -i base --stop-after-init --log-level=warn > "$OUT/init.log" 2>&1
echo 'env.company.write({"country_id": env.ref("base.vn").id, "currency_id": env.ref("base.VND").id}); env.ref("base.VND").active = True; env.cr.commit()' \
  | odoo_run sh -c "odoo shell --db_host=\"\$HOST\" --db_user=\"\$USER\" --db_password=\"\$PASSWORD\" -d $DB --no-http --log-level=error" >> "$OUT/init.log" 2>&1
odoo_run odoo -d "$DB" -i "$MODULES" --stop-after-init --log-level=warn >> "$OUT/init.log" 2>&1
"$ENGINE" run -d --name "$NAME-web" --network "$NET" -e HOST="$NAME-db" -e USER=odoo -e PASSWORD="$PW" \
  -p "127.0.0.1:$PORT:8069" "$IMAGE" odoo -d "$DB" --db-filter="^$DB\$" --http-interface=0.0.0.0 >/dev/null
for _ in $(seq 1 120); do
  curl -fsS -o /dev/null "http://127.0.0.1:$PORT/web/login" 2>/dev/null && break; sleep 3
done

echo "[$(date +%T)] Vietnamese chart, VND, an API key"
"$ENGINE" exec -i "$NAME-web" sh -c "odoo shell --db_host=\"\$HOST\" --db_user=\"\$USER\" --db_password=\"\$PASSWORD\" -d $DB --no-http --log-level=error" \
  < "$HERE/setup.py" > "$OUT/secret/setup.out" 2> "$OUT/setup.log"
grep '^SETUP ' "$OUT/secret/setup.out" || true
sed -n 's/^ASAO_KEY=//p' "$OUT/secret/setup.out" > "$OUT/secret/api-key"
[ -s "$OUT/secret/api-key" ] || { echo "the lab gave no API key; see $OUT/setup.log" >&2; exit 1; }
chmod 600 "$OUT/secret/"*

cat > "$OUT/xdg/aerp/profiles.json" <<JSON
{"profiles": {"review-sim": {"endpoint": "http://127.0.0.1:$PORT", "database": "$DB", "login": "admin", "credentialRef": "review_sim", "protocol": "json2"}}}
JSON
echo "[$(date +%T)] ready on http://127.0.0.1:$PORT (profile review-sim, XDG_CONFIG_HOME=$OUT/xdg)"

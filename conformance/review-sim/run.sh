#!/usr/bin/env bash
# run.sh --out DIR [--engine docker|podman] [--odoo-bin PATH] [--months N] [--scale X] [--mess] [--profile P] [--fresh] [--keep]
#
# The review simulation from start to end, on any machine with Docker or Podman:
# 1. a throwaway Odoo 19 with the Vietnamese chart and no demo data (lab-up.sh);
# 2. a simulated business with its planted cases and look-alikes (simulate.py, in odoo shell);
# 3. odoo-cli review run over the simulated period, through the API, as anyone would;
# 4. the score against the truth (score.py): caught or missed, look-alikes left alone or flagged;
# 5. summary.md. The lab is removed at the end unless --keep. Exits 1 when a planted case was missed or a
#    look-alike flagged, as score.py does.
# --months, --scale and --mess size the business, --profile shapes its sales (trader, retail, service) and
# --fresh adds the held-out set (simulate.py: SIM_MONTHS, SIM_SCALE, SIM_MESS, SIM_PROFILE, SIM_FRESH);
# without them it is the business measured on 2026-10-15.
set -euo pipefail

OUT=""; ENGINE=""; BIN=""; KEEP=false
SIM_MONTHS="${SIM_MONTHS:-6}"; SIM_SCALE="${SIM_SCALE:-1}"; SIM_MESS="${SIM_MESS:-}"; SIM_PROFILE="${SIM_PROFILE:-trader}"; SIM_FRESH="${SIM_FRESH:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --months) SIM_MONTHS="$2"; shift 2 ;;
    --scale) SIM_SCALE="$2"; shift 2 ;;
    --mess) SIM_MESS=1; shift ;;
    --profile) SIM_PROFILE="$2"; shift 2 ;;
    --fresh) SIM_FRESH=1; shift ;;
    --engine) ENGINE="$2"; shift 2 ;;
    --odoo-bin) BIN="$2"; shift 2 ;;
    --keep) KEEP=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$OUT" ] || { echo "usage: $0 --out DIR [--engine docker|podman] [--odoo-bin PATH] [--months N] [--scale X] [--mess] [--profile P] [--fresh] [--keep]" >&2; exit 2; }
ENGINE="${ENGINE:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
trap '[ "$KEEP" = true ] || "$HERE/lab-down.sh" "$ENGINE"' EXIT

if [ -z "$BIN" ]; then
  BIN="$OUT/odoo-cli"
  if [ -f "$HERE/../../../packages/cli/cmd/odoo-cli/main.go" ]; then
    (cd "$HERE/../../../packages/cli" && go build -o "$BIN" ./cmd/odoo-cli)
  else
    (cd "$HERE/../.." && go build -o "$BIN" ./cmd/odoo-cli)
  fi
fi

"$HERE/lab-up.sh" --out "$OUT" --engine "$ENGINE"

echo "[$(date +%T)] simulating the business: $SIM_PROFILE, $SIM_MONTHS months, sales x$SIM_SCALE${SIM_MESS:+, with everyday noise}${SIM_FRESH:+, with the held-out set}"
"$ENGINE" exec -i -e SIM_MONTHS="$SIM_MONTHS" -e SIM_SCALE="$SIM_SCALE" -e SIM_MESS="$SIM_MESS" -e SIM_PROFILE="$SIM_PROFILE" -e SIM_FRESH="$SIM_FRESH" odoo-review-sim-web sh -c 'odoo shell --db_host="$HOST" --db_user="$USER" --db_password="$PASSWORD" -d reviewsim --no-http --log-level=error' \
  < "$HERE/simulate.py" > "$OUT/sim.out" 2> "$OUT/sim.log"
grep '^SIM ' "$OUT/sim.out" || { tail -30 "$OUT/sim.log" >&2; exit 1; }
sed -n 's/^TRUTH=//p' "$OUT/sim.out" > "$OUT/truth.json"
FROM="$(python3 -c "import json; t=json.load(open('$OUT/truth.json')); print(t.get('from', '2026-04-01'))")"
TO="$(python3 -c "import json; t=json.load(open('$OUT/truth.json')); print(t['asOf'])")"

echo "[$(date +%T)] odoo-cli review run --from $FROM --to $TO"
export XDG_CONFIG_HOME="$OUT/xdg" ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true
AERP_CREDENTIAL_REVIEW_SIM="$(cat "$OUT/secret/api-key")"
export AERP_CREDENTIAL_REVIEW_SIM
"$BIN" review run --profile review-sim --from "$FROM" --to "$TO" --markdown-file "$OUT/report.md" --output json > "$OUT/review-result.json" ||
  { echo "the review failed:" >&2; head -c 2000 "$OUT/review-result.json" >&2; exit 1; }
python3 -c 'import json,sys; json.dump(json.load(open(sys.argv[1]))["data"], open(sys.argv[2], "w"), ensure_ascii=False)' \
  "$OUT/review-result.json" "$OUT/review.json"

status=0
python3 -I "$HERE/score.py" --truth "$OUT/truth.json" --review "$OUT/review.json" --markdown "$OUT/score.md" > "$OUT/score.txt" || status=$?
{
  echo "## odoo-cli review, measured on a simulated business"
  echo
  echo "A throwaway Odoo 19 with the Vietnamese chart holds a simulated business, written through Odoo's own flows,"
  echo "with planted errors and frauds and look-alikes that must not be flagged. odoo-cli review run read it through"
  echo "the API, and score.py set its findings against the truth."
  echo
  grep '^SIM ' "$OUT/sim.out" | sed 's/^SIM /- **Simulated:** /'
  echo
  cat "$OUT/score.md"
} > "$OUT/summary.md"
cat "$OUT/score.txt"
exit "$status"

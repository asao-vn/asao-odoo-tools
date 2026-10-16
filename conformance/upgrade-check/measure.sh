#!/usr/bin/env bash
# measure.sh --out DIR [--spec SPEC.json] [--engine docker|podman] [--odoo-bin PATH] [--keep]
#
# Measures `odoo-cli upgrade check --to 20.0` against real Odoo:
# 1. builds a throwaway 19.0 lab (odoo-conformance-lab.sh --setup-only) and plants each customization of
#    the spec there as a person would make it, using it once (plant.sh ... plant);
# 2. runs the check on that database, through the API, as anyone would;
# 3. builds a throwaway 20.0 lab and makes and uses the same customizations there, rolling everything
#    back (plant.sh ... replay);
# 4. sets the check's prediction against what 20.0 did (compare.py) and writes summary.md.
# The spec defaults to the fresh set (make_fresh.py), written after the check and never used to tune it.
# One lab runs at a time. Both are removed at the end; with --keep the 19.0 lab is only stopped and the
# 20.0 lab is left running.
set -euo pipefail

OUT=""; SPEC=""; ENGINE=""; BIN=""; KEEP=false
while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --spec) SPEC="$2"; shift 2 ;;
    --engine) ENGINE="$2"; shift 2 ;;
    --odoo-bin) BIN="$2"; shift 2 ;;
    --keep) KEEP=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$OUT" ] || { echo "usage: $0 --out DIR [--spec SPEC.json] [--engine docker|podman] [--odoo-bin PATH] [--keep]" >&2; exit 2; }
ENGINE="${ENGINE:-$(command -v docker >/dev/null 2>&1 && echo docker || echo podman)}"
export ENGINE
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAB="$HERE/../odoo-conformance-lab.sh"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

lab_gone() {
  "$ENGINE" rm -f -v "odoo-conformance-$1-web" "odoo-conformance-$1-db" >/dev/null 2>&1 || true
  "$ENGINE" network rm "odoo-conformance-$1-net" >/dev/null 2>&1 || true
}
done_with() {
  if [ "$KEEP" != true ]; then
    lab_gone "$1"
  elif [ "$1" = 19 ]; then
    "$ENGINE" stop "odoo-conformance-19-web" "odoo-conformance-19-db" >/dev/null
  fi
}
trap '[ "$KEEP" = true ] || { lab_gone 19; lab_gone 20; }' EXIT

# The lab script's own test of a ready server: a page AND a JSON-RPC call answer.
wait_ready() {
  for _ in $(seq 1 120); do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$1/web/login")" = 200 ] &&
       curl -s -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"call","params":{"service":"common","method":"version","args":[]}}' \
         "http://127.0.0.1:$1/jsonrpc" | grep -q server_serie; then
      return 0
    fi
    sleep 5
  done
  echo "the Odoo web server on port $1 did not answer in 10 minutes" >&2
  exit 1
}

if [ -z "$SPEC" ]; then
  python3 -I "$HERE/make_fresh.py" "$OUT/spec.json"
elif ! [ "$SPEC" -ef "$OUT/spec.json" ]; then
  cp "$SPEC" "$OUT/spec.json"
fi
SPEC="$OUT/spec.json"

echo "[$(date +%T)] 19.0: building the source lab"
"$LAB" --series 19.0 --out "$OUT/lab-19.0" --engine "$ENGINE" ${BIN:+--odoo-bin "$BIN"} --setup-only
BIN="${BIN:-$OUT/lab-19.0/odoo-cli}"
echo "[$(date +%T)] 19.0: planting the customizations, each used once"
"$HERE/plant.sh" 19 "$SPEC" plant "$OUT/plant-19.0.json"
# The web server set up its registry before the planted fields existed; a restart reads them.
"$ENGINE" restart "odoo-conformance-19-web" >/dev/null
wait_ready 58739
echo "[$(date +%T)] 19.0: odoo-cli upgrade check --to 20.0"
XDG_CONFIG_HOME="$OUT/lab-19.0/xdg" ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true \
  AERP_CREDENTIAL_CONFORMANCE="$(cat "$OUT/lab-19.0/secret/api-key")" \
  "$BIN" upgrade check --profile conformance --to 20.0 --markdown-file "$OUT/report-vi.md" > "$OUT/findings.json" ||
  { echo "the check failed:" >&2; cat "$OUT/findings.json" >&2; exit 1; }
done_with 19

echo "[$(date +%T)] 20.0: building the target lab"
"$LAB" --series 20.0 --out "$OUT/lab-20.0" --engine "$ENGINE" --odoo-bin "$BIN" --setup-only
echo "[$(date +%T)] 20.0: making and using the same customizations, rolled back"
"$HERE/plant.sh" 20 "$SPEC" replay "$OUT/replay-20.0.json"
done_with 20

python3 -I "$HERE/compare.py" "$SPEC" "$OUT/findings.json" "$OUT/replay-20.0.json" > "$OUT/compare.txt"
python3 -I "$HERE/summary.py" "$SPEC" "$OUT/plant-19.0.json" "$OUT/findings.json" "$OUT/compare.txt" > "$OUT/summary.md"
cat "$OUT/compare.txt"
echo "[$(date +%T)] done: $OUT/summary.md"

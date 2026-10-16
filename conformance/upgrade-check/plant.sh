#!/bin/sh
# plant.sh <19|20> <spec.json> <plant|replay> <results.json>: creates and uses each customization of the
# spec in lab odoo-conformance-<series>; plant commits them, replay rolls everything back.
HERE="$(cd "$(dirname "$0")" && pwd)"
RUN="$(mktemp)"
python3 - "$2" "$3" > "$RUN" <<'PY'
import sys
print("import json")
print("SPEC = json.loads(%r)" % open(sys.argv[1], encoding="utf-8").read())
print("MODE = %r" % sys.argv[2])
PY
cat "$HERE/plant_body.py" >> "$RUN"
"$HERE/shell.sh" "$1" "$RUN" RESULTS "$4"
status=$?
rm -f "$RUN"
exit $status

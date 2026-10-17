#!/usr/bin/env bash
# Remove the review-simulation lab's containers and network (the database goes with them).
set -euo pipefail
ENGINE="${1:-$(command -v podman >/dev/null 2>&1 && echo podman || echo docker)}"
"$ENGINE" rm -f odoo-review-sim-web odoo-review-sim-db >/dev/null 2>&1 || true
"$ENGINE" network rm odoo-review-sim-net >/dev/null 2>&1 || true

#!/bin/sh
# shell.sh <19|20> <script.py> <marker> <out>: runs a script in lab odoo-conformance-<series>'s odoo shell
# and keeps what it printed between <marker>-BEGIN and <marker>-END. ENGINE (default podman) runs the lab.
"${ENGINE:-podman}" exec -i "odoo-conformance-$1-web" sh -c 'odoo shell -c /etc/odoo/odoo.conf --db_host="$HOST" --db_user="$USER" --db_password="$PASSWORD" -d conformance --no-http --log-level=error' \
  < "$2" > "$4.raw" 2>&1
sed -n "/^$3-BEGIN\$/,/^$3-END\$/p" "$4.raw" | sed '1d;$d' > "$4"
[ -s "$4" ] && rm -f "$4.raw" || { echo "no $3 output; see $4.raw" >&2; exit 1; }

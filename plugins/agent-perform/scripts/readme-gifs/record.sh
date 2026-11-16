#!/usr/bin/env bash
# Re-record the dashboard GIFs in readme-assets/ from synthetic transcripts.
#
#   scripts/readme-gifs/record.sh <workdir> [overview|pages|topic|drill|verify ...]
#
# The dashboard runs on :8797 with HOME=<workdir>/home, so the real ~/.claude and the :8787 dashboard are
# never read. Needs Google Chrome and python3; Pillow and websocket-client go into <workdir>/venv.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
plugin="$(cd "$here/../.." && pwd)"
work="$(mkdir -p "${1:?usage: record.sh <workdir> [name ...]}" && cd "$1" && pwd)"
shift
port=8797
now=1794823200  # 2026-11-16 18:00 +08:00, the "today" gen_transcripts.py writes up to

[ -x "$work/venv/bin/python" ] || { python3 -m venv "$work/venv" && "$work/venv/bin/pip" -q install Pillow websocket-client; }
if lsof -ti "tcp:$port" -sTCP:LISTEN >/dev/null; then echo "port $port is busy" >&2; exit 1; fi

rm -rf "$work/home" && mkdir -p "$work/home"
python3 -I "$here/gen_transcripts.py" "$work/home"
env -i HOME="$work/home" AP_NOW=$now PATH=/usr/bin:/bin "$plugin/bin/ap" session-end
env -i HOME="$work/home" AP_NOW=$now PATH=/usr/bin:/bin "$plugin/bin/ap" dashboard --serve "$port" >"$work/dash.log" 2>&1 &
dash=$!
trap 'kill $dash 2>/dev/null' EXIT
for _ in $(seq 40); do curl -s -o /dev/null "http://127.0.0.1:$port/" && break; sleep 0.5; done

"$work/venv/bin/python" "$here/record.py" "$work" "$plugin/readme-assets" "$@"
"$here/check.sh" "$work"

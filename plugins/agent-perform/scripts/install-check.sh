#!/usr/bin/env bash
# Install-from-marketplace check for this plugin. Name-agnostic: the plugin name comes from plugin.json,
# the marketplace name from the repo's .claude-plugin/marketplace.json.
#
#   scripts/install-check.sh <plugin dir>
#
# Everything runs in a temp HOME / CLAUDE_CONFIG_DIR; the real ~/.claude is never touched.
# No `claude -p`, and the real dashboard on :8787 is never contacted, stopped or restarted (the hook that
# would do that is resolve-only, see the SKIP lines).
set -u
export PATH="$PATH:/usr/local/bin:/opt/homebrew/bin"

plugin_dir="$(cd "${1:?usage: install-check.sh <plugin dir>}" && pwd)"
repo="$(git -C "$plugin_dir" rev-parse --show-toplevel)"
name="$(jq -r .name "$plugin_dir/.claude-plugin/plugin.json")"
version="$(jq -r .version "$plugin_dir/.claude-plugin/plugin.json")"
mkt="$(jq -r .name "$repo/.claude-plugin/marketplace.json")"

T="$(mktemp -d /tmp/install-check.XXXXXX)"; trash="$HOME/.Trash"
trap 'mkdir -p "$trash"; mv "$T" "$trash/install-check.$$"' EXIT
export HOME="$T" CLAUDE_CONFIG_DIR="$T/.claude"
mkdir -p "$CLAUDE_CONFIG_DIR"
inst="$CLAUDE_CONFIG_DIR/plugins/cache/$mkt/$name/$version"
fails=0
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }
skip() { echo "SKIP  $*"; }
ok()   { local label="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$label"; else fail "$label"; fi; }

echo "plugin=$name version=$version marketplace=$mkt repo=$repo temp=$T"

# the real dashboard must stay untouched: deny outbound to it for every hook run
SB='(version 1)(allow default)(deny network-outbound (remote ip "localhost:8787"))'
if command -v sandbox-exec >/dev/null; then sb() { sandbox-exec -p "$SB" "$@"; }; else
  sb() { "$@"; }; echo "WARN  sandbox-exec missing: hooks may reach the real :8787 dashboard"; fi

# ── 1. marketplace add + install + list ──────────────────────────────────────
ok "marketplace lists $name with source $plugin_dir" \
  jq -e --arg n "$name" --arg d "$plugin_dir" --arg r "$repo" \
  '.plugins[] | select(.name==$n) | (.source | ltrimstr("./")) as $s | ($r + "/" + $s) == $d' "$repo/.claude-plugin/marketplace.json"
claude plugin marketplace add "$repo" >"$T/add.log" 2>&1; ok "marketplace add $repo" test $? -eq 0
claude plugin install "$name@$mkt" >"$T/install.log" 2>&1; ok "install $name@$mkt" test $? -eq 0
listed="$(claude plugin list --json 2>/dev/null)"
ok "plugin list: $name@$mkt enabled at $version" \
  jq -e --arg id "$name@$mkt" --arg v "$version" '.[] | select(.id==$id and .enabled==true and .version==$v)' <<<"$listed"
ok "installed copy exists at cache/$mkt/$name/$version" test -x "$inst/bin/ap"
ok "installed copy == repo plugin dir (ignoring .DS_Store)" \
  diff -rq -x .DS_Store -x __pycache__ -x .git "$plugin_dir" "$inst"
[ -d "$inst" ] || { echo "no installed copy, cannot continue"; exit 1; }

# ── 2. hooks, from the installed copy ────────────────────────────────────────
hooks="$inst/hooks/hooks.json"
export CLAUDE_PLUGIN_ROOT="$inst"
unresolved=0
while IFS= read -r c; do
  p="${c//\$\{CLAUDE_PLUGIN_ROOT\}/$inst}"; p="${p#\"}"; p="${p%%\"*}"
  [ -x "$p" ] || { unresolved=1; echo "      unresolved: $c"; }
done < <(jq -r '.hooks[][].hooks[].command' "$hooks")
ok "every hooks.json command resolves to an executable in the install" test $unresolved -eq 0
for m in $(jq -r '.modules[]' "$hooks"); do ok "hook module $m exists" test -f "$inst/hooks/$m"; done

cmd_of() { jq -r --arg e "$1" --arg s "$2" '.hooks[$e][].hooks[] | select(.command | contains($s)) | .command' "$hooks" | head -1; }
tmo_of() { jq -r --arg e "$1" --arg s "$2" '.hooks[$e][].hooks[] | select(.command | contains($s)) | .timeout // 10' "$hooks" | head -1; }

tp="$T/transcript.jsonl"
cat >"$tp" <<EOF
{"type":"user","sessionId":"apchk-1","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","sessionId":"apchk-1","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hi"}]}}
EOF
base="\"session_id\":\"apchk-1\",\"transcript_path\":\"$tp\",\"cwd\":\"$T\""

# run_hook <label> <event> <command substring> <stdin json> <expected rc> [extra env assignment]
run_hook() {
  local label="$1" ev="$2" sub="$3" in="$4" want="$5" extra="${6:-}" c to t0 t1 rc ms
  c="$(cmd_of "$ev" "$sub")"; to="$(tmo_of "$ev" "$sub")"
  [ -n "$c" ] || { fail "$label: no such hook command"; return; }
  c="${c//\$\{CLAUDE_PLUGIN_ROOT\}/$inst}"
  t0=$(perl -MTime::HiRes=time -e 'printf "%d", time*1000')
  ( [ -z "$extra" ] || export "$extra"; printf '%s' "$in" | sb perl -e 'alarm 20; exec @ARGV' bash -c "$c" >"$T/out" 2>"$T/err" ); rc=$?
  t1=$(perl -MTime::HiRes=time -e 'printf "%d", time*1000'); ms=$((t1 - t0))
  if [ "$rc" -eq "$want" ] && [ "$ms" -lt $((to * 1000)) ]; then pass "$label (rc=$rc, ${ms}ms < ${to}s)"
  else fail "$label (rc=$rc want $want, ${ms}ms, timeout ${to}s) stderr: $(head -c 200 "$T/err")"; fi
}

run_hook "SessionStart nudge" SessionStart "session-start nudge" "{$base,\"hook_event_name\":\"SessionStart\"}" 0
ok "nudge is JSON and names /$name:setup" jq -e --arg s "/$name:setup" '.systemMessage|contains($s)' "$T/out"
# this one would kill/replace the real dashboard: resolve-only
c="$(cmd_of SessionStart "session-start dashboard")"; c="${c//\$\{CLAUDE_PLUGIN_ROOT\}/$inst}"
ok "SessionStart 'session-start dashboard' binary runs" bash -c "${c%% *} --version"
skip "SessionStart 'session-start dashboard' not executed (kills the listener on :8787)"
ok "no UserPromptSubmit hook (nothing is injected per turn)" jq -e '.hooks | has("UserPromptSubmit") | not' "$hooks"

run_hook "PreToolUse Bash 'sleep 60' foreground is blocked (rc 2)" PreToolUse poll-gate \
  "{$base,\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"sleep 60\"}}" 2
ok "poll-gate says why on stderr" test -s "$T/err"
run_hook "PreToolUse Bash 'ls' passes" PreToolUse poll-gate \
  "{$base,\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"ls\"}}" 0

# ctx-probe would run `claude -p /context`: the plugin's own recursion guard turns it into a no-op
run_hook "Stop ctx-probe (guard set, so no claude -p)" Stop ctx-probe "{$base,\"hook_event_name\":\"Stop\"}" 0 CLAUDE_CTX_PROBE=1
skip "Stop ctx-probe real path (claude -p /context) not checked"
run_hook "SessionEnd (nightly refresh in the temp HOME)" SessionEnd session-end "{$base,\"hook_event_name\":\"SessionEnd\"}" 0

# ── 3. statusline command from skills/setup/SKILL.md ─────────────────────────
sl="$(jq -r '.statusLine.command' < <(sed -n '/^```json/,/^```/p' "$inst/skills/setup/SKILL.md" | grep -v '^```') 2>/dev/null)"
ok "setup skill documents a statusLine command for cache/*/$name/" bash -c "[[ '$sl' == *'cache/*/$name/'* ]]"
sin="{\"session_id\":\"apchk-1\",\"transcript_path\":\"$tp\",\"cwd\":\"$T\",\"model\":{\"id\":\"claude-sonnet-4-5\",\"display_name\":\"Sonnet\"},\"workspace\":{\"current_dir\":\"$T\"}}"
sline="$(printf '%s' "$sin" | sb perl -e 'alarm 20; exec @ARGV' bash -c "$sl" 2>"$T/err")"; rc=$?
if [ $rc -eq 0 ] && [ -n "$sline" ]; then pass "statusline prints a line via the cache glob: $(head -c 120 <<<"$sline")"
else fail "statusline rc=$rc output='$sline' stderr=$(head -c 200 "$T/err")"; fi

# ── 4. namespaces and references ─────────────────────────────────────────────
det="$(claude plugin details "$name" 2>&1)"
have="$(cd "$inst/skills" && ls -d */ | tr -d / | sort | paste -sd, -)"
listed_names="$(grep -E "^ +Skills \(" <<<"$det" | sed 's/.*)  *//' | tr -d ' ' | tr ',' '\n' | sort | paste -sd, -)"
ok "claude plugin details lists skills: $have" test "$have" = "$listed_names"
ok "no bundled subagents" test ! -d "$inst/agents"
for f in "$inst"/skills/*/SKILL.md; do
  fm="$(sed -n 's/^name: *//p' "$f" | head -1)"; want="$(basename "$(dirname "$f")")"
  ok "$name:$want frontmatter name matches" test "$fm" = "$want"
done
bad="$(grep -rEoh '(^|[^A-Za-z0-9_./-])/[a-z][a-z0-9-]*:[a-z][a-z-]*' "$inst/skills" "$inst/README.md" | sed 's/^[^/]*//' | sort -u | grep -v "^/$name:" || true)"
ok "every /x:y in skills + README uses /$name: (others: ${bad:-none})" test -z "$bad"
badmkt="$(grep -rEoh '[a-z0-9-]+@[a-z0-9-]*marketplace' "$inst/skills" "$inst/README.md" | sort -u | grep -vx "$name@$mkt" || true)"
ok "every plugin@marketplace in skills + README is $name@$mkt (others: ${badmkt:-none})" test -z "$badmkt"
ok "claude plugin validate $inst" claude plugin validate "$inst"

# ── 5. the Mod ───────────────────────────────────────────────────────────────
claude plugin test "$inst" >"$T/modtest.log" 2>&1; rc=$?
if [ $rc -eq 0 ]; then pass "claude plugin test (mod): $(tail -n 3 "$T/modtest.log" | tr '\n' ' ' | head -c 160)"
else fail "claude plugin test rc=$rc: $(tail -n 8 "$T/modtest.log")"; fi

# ── 6. summary ───────────────────────────────────────────────────────────────
echo "---- $name: $([ $fails -eq 0 ] && echo PASS || echo "FAIL ($fails)")"
[ $fails -eq 0 ]

"""Write synthetic Claude Code transcripts under <home>/.claude/projects for the README GIFs.

usage: python gen_transcripts.py <home>
Seeded, so every run writes the same sessions. Nothing comes from real transcripts: projects live
under /Users/dev/projects, and usage is derived from the generated content so the numbers line up.
"""
import json, os, random, sys, uuid
from datetime import datetime, timedelta, timezone

HOME = sys.argv[1]
R = random.Random(20261116)
LOCAL = timezone(timedelta(hours=8))
TODAY = datetime(2026, 11, 16, tzinfo=LOCAL)
CHANGE_DAY = TODAY - timedelta(days=39)
DEV = "/Users/dev"
PREFIX = 14_200  # system prompt + tool schemas, never in the transcript

PROJECTS = {
    "data-etl": {
        "files": ["src/loader.py", "src/transform.py", "src/schema.py", "src/config.py", "src/csv_reader.py",
                  "tests/test_loader.py", "tests/test_csv_reader.py", "docs/config.md"],
        "prompts": ["document the config options", "split the transform into two steps", "write a test for the CSV reader",
                    "加上資料欄位的型別檢查", "add a retry to the loader", "why does the loader skip empty rows",
                    "rename the schema fields to snake_case", "把 transform 的錯誤訊息寫清楚一點"],
        "follow": ["no, keep the old names, only change the schema", "這樣太慢了，換個做法", "also cover the empty file case",
                   "run the tests again"],
    },
    "shop-api": {
        "files": ["src/cart/service.ts", "src/coupon/total.ts", "src/checkout/handler.ts", "src/store/todos.ts",
                  "src/orders/export.ts", "tests/cart.test.ts", "tests/coupon.test.ts"],
        "prompts": ["check why the coupon total is wrong", "write tests for the cart service", "新增訂單匯出功能",
                    "why does checkout return a 500 on empty carts", "add rate limiting to the checkout route",
                    "把購物車的金額計算抽成一個函式"],
        "follow": ["no, keep the old names, only change the store", "that is not what I meant", "這樣太慢了，換個做法",
                   "ok, now update the docs"],
    },
    "blog-web": {
        "files": ["src/posts/list.tsx", "src/posts/links.ts", "src/auth/LoginForm.tsx", "src/date/parser.ts",
                  "src/posts/Post.tsx", "tests/parser.test.ts"],
        "prompts": ["fix the date parser", "validate the links in every post", "add pagination to the post list",
                    "加上登入頁的表單驗證", "make the post page load faster"],
        "follow": ["that is not what I meant", "also handle time zones", "看起來可以，再補個測試"],
    },
}
WORDS = ("value row field items total config parse load result schema record cart order coupon price post link "
         "date token user session cache retry buffer stream reader writer index limit offset").split()


def code_lines(n, path):
    ext = path.rsplit(".", 1)[-1]
    out = []
    for i in range(n):
        a, b, c = R.choice(WORDS), R.choice(WORDS), R.choice(WORDS)
        if ext == "py":
            line = R.choice([f"def {a}_{b}({c}):", f"    {a} = {b}.get('{c}')", f"    if not {a}:", f"        return {b}",
                             f"    for {a} in {b}:", f"        {c}.append({a})", f"# {a} {b}", ""])
        elif ext in ("ts", "tsx"):
            line = R.choice([f"export function {a}{b.title()}({c}: number) {{", f"  const {a} = {b}.{c};",
                             f"  if (!{a}) return {b};", f"  return {a}.map(({c}) => {c}.{b});", "}",
                             f"import {{ {a} }} from './{b}';", ""])
        else:
            line = R.choice([f"## {a.title()} {b}", f"- `{a}`: the {b} {c}", f"The {a} option sets the {b}.", ""])
        out.append(f"{i + 1:6}\t{line}")
    return "\n".join(out)


def est(text):
    cjk = sum(1 for ch in text if "一" <= ch <= "鿿")
    return int(cjk * 0.7 + (len(text) - cjk) / 4)


def iso(t):
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{t.microsecond // 1000:03d}Z"


def rid(prefix, n=24):
    return prefix + "".join(R.choice("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") for _ in range(n))


def uid():
    return str(uuid.UUID(int=R.getrandbits(128), version=4))


class Writer:
    """One transcript file: tracks context size and cache state so usage matches content."""

    def __init__(self, sid, proj, model, ttl, sidechain=False, agent_id=None):
        self.sid, self.proj, self.model, self.ttl = sid, proj, model, ttl
        self.cwd = f"{DEV}/projects/{proj}"
        self.rows, self.parent = [], None
        self.ctx, self.cached, self.last_call = PREFIX, 0, None
        self.side, self.agent_id = sidechain, agent_id

    def row(self, t, typ, **kw):
        u = uid()
        r = {"parentUuid": self.parent, "isSidechain": self.side, "userType": "external", "cwd": self.cwd,
             "sessionId": self.sid, "version": "2.3.12", "gitBranch": "main", "type": typ, "uuid": u, "timestamp": iso(t)}
        if self.agent_id:
            r["agentId"] = self.agent_id
        r.update(kw)
        self.rows.append(r)
        self.parent = u
        return r

    def attach(self, t, att):
        self.row(t, "attachment", attachment=att)

    def user(self, t, text, **kw):
        self.ctx += est(text)
        self.row(t, "user", message={"role": "user", "content": text}, **kw)

    def call(self, t, parts, out_tok):
        ttl_s = 3600 if self.ttl == "1h" else 300
        if self.last_call is None or (t - self.last_call).total_seconds() > ttl_s:
            self.cached = 0
        seat = self.ctx
        inp = R.randint(1, 6)
        cr = min(self.cached, seat - inp)
        cw = max(seat - cr - inp, 0)
        usage = {"input_tokens": inp, "cache_creation_input_tokens": cw, "cache_read_input_tokens": cr,
                 "output_tokens": out_tok,
                 "cache_creation": {"ephemeral_1h_input_tokens": cw if self.ttl == "1h" else 0,
                                    "ephemeral_5m_input_tokens": cw if self.ttl == "5m" else 0},
                 "service_tier": "standard"}
        self.row(t, "assistant", requestId=rid("req_"),
                 message={"id": rid("msg_"), "type": "message", "role": "assistant", "model": self.model,
                          "content": parts, "stop_reason": "tool_use" if any(p["type"] == "tool_use" for p in parts) else "end_turn",
                          "usage": usage})
        self.cached = seat
        self.last_call = t
        self.ctx = seat + sum(est(json.dumps(p, ensure_ascii=False)) for p in parts)

    def result(self, t, tu_id, text, tur, is_error=False):
        part = {"type": "tool_result", "tool_use_id": tu_id, "content": text}
        if is_error:
            part["is_error"] = True
        self.ctx += est(text)
        self.row(t, "user", message={"role": "user", "content": [part]}, toolUseResult=tur)

    def save(self, path, mtime):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            for r in self.rows:
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
        os.utime(path, (mtime, mtime))


def tool_use(name, inp):
    return {"type": "tool_use", "id": rid("toolu_"), "name": name, "input": inp}


def text_part(s):
    return {"type": "text", "text": s}


REPLIES = ["Done. The change is in place and the tests pass.", "I found the cause and fixed it.",
           "Updated the file; here is what changed.", "改好了，測試都通過。", "The tests pass now."]


def do_tools(w, t, proj, fp_base, big, spec):
    """Run a short chain of tool calls inside a turn; returns the time after the last result."""
    files = PROJECTS[proj]["files"]
    for step in spec:
        if w.ctx > 186_000:
            break
        big_now = big and w.ctx < 140_000
        t += timedelta(seconds=R.randint(4, 30))
        f = R.choice(files)
        path = f"{w.cwd}/{f}"
        if step == "read":
            tu = tool_use("Read", {"file_path": path})
            w.call(t, [text_part("Let me read the file."), tu], R.randint(80, 400))
            t += timedelta(seconds=R.randint(1, 3))
            n = R.randint(60, 260) * (6 if big_now else 1)
            w.result(t, tu["id"], code_lines(n, f), {"type": "text", "file": {"filePath": path, "numLines": n}})
        elif step == "reread":
            tu = tool_use("Read", {"file_path": fp_base})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], code_lines(R.randint(80, 180), fp_base), {"type": "text", "file": {"filePath": fp_base}})
        elif step == "bigread":
            tu = tool_use("Read", {"file_path": path})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=1)
            msg = f"File content (31842 tokens) exceeds maximum allowed tokens (25000). Please use offset and limit parameters to read specific portions of the file, or use the GrepTool to search for specific content."
            w.result(t, tu["id"], f"<tool_use_error>{msg}</tool_use_error>", f"Error: {msg}", is_error=True)
            t += timedelta(seconds=R.randint(3, 9))
            tu = tool_use("Read", {"file_path": path, "offset": 1, "limit": 600})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], code_lines(600, f), {"type": "text", "file": {"filePath": path, "numLines": 600}})
        elif step == "grep":
            tu = tool_use("Grep", {"pattern": R.choice(WORDS), "path": w.cwd, "output_mode": "content"})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=1)
            n = R.randint(30, 120) * (12 if big_now else 1)
            lines = [f"{w.cwd}/{R.choice(files)}:{R.randint(1, 400)}:  {R.choice(WORDS)} = {R.choice(WORDS)}.{R.choice(WORDS)}" for _ in range(n)]
            w.result(t, tu["id"], "\n".join(lines), {"mode": "content", "numLines": n})
        elif step == "glob":
            tu = tool_use("Glob", {"pattern": "**/*." + f.rsplit(".", 1)[-1]})
            w.call(t, [tu], R.randint(40, 120))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], "\n".join(f"{w.cwd}/{x}" for x in files), {"filenames": files, "numFiles": len(files)})
        elif step == "edit":
            tu = tool_use("Edit", {"file_path": path, "old_string": f"{R.choice(WORDS)} = {R.choice(WORDS)}",
                                   "new_string": f"{R.choice(WORDS)} = {R.choice(WORDS)}({R.choice(WORDS)})"})
            w.call(t, [tu], R.randint(200, 900))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], f"The file {path} has been updated successfully.", {"filePath": path})
        elif step == "editfail":
            tu = tool_use("Edit", {"file_path": path, "old_string": "return total", "new_string": "return round(total, 2)"})
            w.call(t, [tu], R.randint(100, 300))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], "<tool_use_error>String to replace not found in file.\nString: return total</tool_use_error>",
                     "Error: String to replace not found in file.", is_error=True)
            t += timedelta(seconds=R.randint(3, 8))
            tu = tool_use("Read", {"file_path": path})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=1)
            w.result(t, tu["id"], code_lines(150, f), {"type": "text", "file": {"filePath": path}})
        elif step == "test":
            cmd = "pytest -q" if proj == "data-etl" else "npm test"
            tu = tool_use("Bash", {"command": cmd, "description": "Run the tests"})
            w.call(t, [tu], R.randint(60, 200))
            t += timedelta(seconds=R.randint(5, 40))
            n = R.randint(8, 40)
            out = "\n".join(f"  ok {i + 1} {R.choice(WORDS)} {R.choice(WORDS)}" for i in range(n)) + f"\n{n} passed"
            w.result(t, tu["id"], out, {"stdout": out, "stderr": "", "interrupted": False})
        elif step == "script":
            code = f"import json\nrows = json.load(open('{w.cwd}/data/sample.json'))\nprint(len(rows), sorted(rows[0]))\n"
            tu = tool_use("Bash", {"command": f"python3 - <<'EOF'\n{code}EOF", "description": "Count sample rows"})
            w.call(t, [tu], R.randint(150, 400))
            t += timedelta(seconds=2)
            out = f"{R.randint(100, 900)} ['id', 'name', 'total']"
            w.result(t, tu["id"], out, {"stdout": out, "stderr": "", "interrupted": False})
    return t


def subagent(w, t, sess_dir, kind, task):
    """Launch one Agent call with its own sidechain transcript."""
    aid = "a" + "".join(R.choice("0123456789abcdef") for _ in range(16))
    tu = tool_use("Agent", {"description": task[:40], "subagent_type": kind, "prompt": task})
    w.call(t, [text_part(f"I'll hand this to the {kind} subagent."), tu], R.randint(200, 500))
    model = "claude-sonnet-5-5" if kind != "Explore" else "claude-haiku-5-5"
    sw = Writer(w.sid, w.proj, model, "5m", sidechain=True, agent_id=aid)
    st = t + timedelta(seconds=2)
    sw.user(st, task)
    spec = R.choice([["read", "grep", "read"], ["glob", "read", "edit", "test"], ["grep", "read", "read", "edit"]]) * R.choice([2, 3])
    st = do_tools(sw, st, w.proj, f"{w.cwd}/{PROJECTS[w.proj]['files'][0]}", True, spec)
    st += timedelta(seconds=R.randint(5, 20))
    summary = f"Finished: {task}. Changed 2 files; tests pass."
    sw.call(st, [text_part(summary)], R.randint(200, 800))
    sw.save(os.path.join(sess_dir, "subagents", f"agent-{aid}.jsonl"), st.timestamp())
    dur = int((st - t).total_seconds() * 1000)
    end = st + timedelta(seconds=1)
    w.result(end, tu["id"], f"{summary}\nagentId: {aid} (use SendMessage to continue)",
             {"status": "completed", "agentId": aid, "prompt": task, "totalDurationMs": dur, "totalTokens": sw.ctx,
              "totalToolUseCount": len(spec), "resolvedModel": model, "content": [text_part(summary)]})
    return end


def start_attachments(w, t, day):
    after = day >= CHANGE_DAY
    claude_md = ["# Working rules", "- Reply in the language the user writes in.", "- Keep diffs small.",
                 "- Run the tests before saying a change is done."]
    if after:
        claude_md += ["- Run /clear between unrelated tasks.", "- Read only the lines you need; use offset and limit on big files.",
                      "- Hand large searches to an Explore subagent."]
    proj_md = [f"# {w.proj}", "Run the tests with the project's test command.", "Source lives in src/."]
    w.attach(t, {"type": "instructions", "files": [
        {"path": f"{DEV}/.claude/CLAUDE.md", "content": "\n".join(claude_md)},
        {"path": f"{w.cwd}/CLAUDE.md", "content": "\n".join(proj_md)}]})
    skills = ["review", "commit", "refactor"] + (["tdd"] if after else [])
    w.attach(t, {"type": "skill_listing", "isInitial": True, "names": skills, "content": ", ".join(skills)})
    w.attach(t, {"type": "model", "identity": {"modelId": w.model}})
    w.ctx += 900 if after else 700


def session(day, proj, kind):
    """kind: normal | long | gap | deleg"""
    sid = uid()
    before = day < CHANGE_DAY
    model = "claude-opus-5-5" if (R.random() < (0.85 if before else 0.6) or kind == "long") else "claude-sonnet-5-5"
    ttl = "1h" if R.random() < 0.7 else "5m"
    w = Writer(sid, proj, model, ttl)
    pdir = os.path.join(HOME, ".claude", "projects", "-Users-dev-projects-" + proj)
    path = os.path.join(pdir, sid + ".jsonl")
    sess_dir = os.path.join(pdir, sid)
    t = day.replace(hour=R.randint(9, 18), minute=R.randint(0, 59), second=R.randint(0, 59))
    start_attachments(w, t, day)
    P = PROJECTS[proj]
    first = R.choice(P["prompts"])
    nturns = {"normal": R.randint(2, 5), "long": R.randint(5, 7), "gap": R.randint(3, 5), "deleg": R.randint(3, 6)}[kind]
    if before and kind == "normal":
        nturns += R.randint(1, 3)
    fp_base = f"{w.cwd}/{P['files'][0]}"
    gap_at = R.randint(1, nturns - 1) if kind == "gap" else -1
    for i in range(nturns):
        t += timedelta(seconds=R.randint(2, 20))
        prompt = first if i == 0 else R.choice(P["follow"] + P["prompts"])
        w.user(t, prompt)
        big = kind == "long" or (before and R.random() < 0.25)
        spec = R.choice([["read", "edit", "test"], ["grep", "read", "edit", "edit"], ["glob", "read", "read", "edit"],
                         ["read", "grep", "edit"], ["read", "edit"], ["grep", "read", "edit", "test"]])
        if big:
            spec = spec * R.choice([1, 1, 2, 3, 4])
        if R.random() < 0.05:
            spec = ["bigread"] + spec
        if R.random() < 0.06:
            spec = spec[:1] + ["editfail"] + spec[1:]
        if R.random() < 0.06:
            spec = ["script"] + spec
        if i > 0 and R.random() < 0.2:
            spec = ["reread"] + spec
        t = do_tools(w, t, proj, fp_base, big, spec)
        if kind == "deleg" and i in (0, 2) or (kind == "normal" and not before and R.random() < 0.25):
            sk = R.choice(["implementer", "reviewer", "Explore"])
            task = R.choice(["review the diff for bugs", "implement the change in the listed files",
                             "find every caller of the parser", "write the missing tests"])
            t = subagent(w, t + timedelta(seconds=5), sess_dir, sk, task)
        if R.random() < 0.07 and i < nturns - 1:
            t += timedelta(seconds=R.randint(3, 20))
            w.row(t, "user", message={"role": "user", "content": [text_part("[Request interrupted by user]")]})
            continue
        t += timedelta(seconds=R.randint(5, 40))
        w.call(t, [text_part(R.choice(REPLIES))], R.randint(150, 900))
        if i == gap_at:
            t += timedelta(minutes=R.randint(75, 140) if ttl == "1h" else R.randint(9, 40))
        else:
            t += timedelta(seconds=R.randint(40, 600))
    w.save(path, t.timestamp())
    return t


def main():
    days = []
    for back in range(89, -1, -1):
        d = TODAY - timedelta(days=back)
        if d.weekday() >= 5 and R.random() < 0.7:
            continue
        if R.random() < 0.38 and d.date() != CHANGE_DAY.date():
            continue
        days.append(d)
    n = 0
    for d in days:
        before = d < CHANGE_DAY
        for _ in range(R.choice([1, 1, 2, 2, 3])):
            proj = R.choice(list(PROJECTS))
            roll = R.random()
            if before:
                kind = "long" if roll < 0.28 else "gap" if roll < 0.42 else "deleg" if roll < 0.5 else "normal"
            else:
                kind = "long" if roll < 0.08 else "gap" if roll < 0.15 else "deleg" if roll < 0.35 else "normal"
            session(d, proj, kind)
            n += 1
    print(f"{n} sessions over {len(days)} days")


main()

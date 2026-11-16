"""Minimal headless-Chrome driver over CDP for recording README GIFs."""
import base64, io, json, math, subprocess, time, urllib.request
import websocket
from PIL import Image

CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
W, H = 1280, 1000
CURSOR_JS = """
(() => {
  if (document.getElementById('__cur')) return;
  const c = document.createElement('div'); c.id = '__cur';
  Object.assign(c.style, {position:'fixed', left:'-40px', top:'-40px', width:'22px', height:'22px', borderRadius:'50%',
    background:'rgba(59,130,246,.45)', border:'2px solid rgba(37,99,235,.9)', zIndex:2147483647, pointerEvents:'none',
    transform:'translate(-50%,-50%)', transition:'none'});
  document.documentElement.appendChild(c);
})()
"""


class Browser:
    def __init__(self, profile, port=9333):
        self.proc = subprocess.Popen([CHROME, "--headless=new", f"--remote-debugging-port={port}", f"--user-data-dir={profile}",
                                      f"--window-size={W},{H}", "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
                                      "--force-device-scale-factor=1", "--lang=zh-TW", "about:blank"],
                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(50):
            try:
                tabs = json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/json"))
                page = next(t for t in tabs if t["type"] == "page")
                break
            except Exception:
                time.sleep(0.2)
        self.ws = websocket.create_connection(page["webSocketDebuggerUrl"], max_size=None)
        self.n = 0
        self.cmd("Page.enable")
        self.cmd("Emulation.setDeviceMetricsOverride", width=W, height=H, deviceScaleFactor=1, mobile=False)
        self.frames = []
        self.texts = []
        self.cur = (W // 2, H // 2)

    def cmd(self, method, **params):
        self.n += 1
        my = self.n
        self.ws.send(json.dumps({"id": my, "method": method, "params": params}))
        while True:
            msg = json.loads(self.ws.recv())
            if msg.get("id") == my:
                if "error" in msg:
                    raise RuntimeError(f"{method}: {msg['error']}")
                return msg.get("result", {})

    def js(self, expr):
        r = self.cmd("Runtime.evaluate", expression=expr, returnByValue=True, awaitPromise=True)
        if "exceptionDetails" in r:
            raise RuntimeError(f"js failed: {expr[:80]}: {r['exceptionDetails']}")
        return r.get("result", {}).get("value")

    def goto(self, url, settle=1.2):
        self.cmd("Page.navigate", url=url)
        for _ in range(100):
            if self.js("document.readyState") == "complete":
                break
            time.sleep(0.1)
        time.sleep(settle)
        self.js(CURSOR_JS)
        self.place(*self.cur)
        self.snapshot()

    def snapshot(self):
        self.texts.append(self.js("location.href + '\\n' + document.body.innerText + '\\n' + document.documentElement.outerHTML"))

    def place(self, x, y):
        self.cur = (x, y)
        self.js(f"(()=>{{const c=document.getElementById('__cur'); if(c){{c.style.left='{x}px';c.style.top='{y}px'}}}})()")

    def shot(self):
        data = self.cmd("Page.captureScreenshot", format="png")["data"]
        return Image.open(io.BytesIO(base64.b64decode(data))).convert("RGB")

    def frame(self, count=1):
        img = self.shot()
        for _ in range(count):
            self.frames.append(img)

    def hold(self, frames):
        self.frame(frames)

    def center(self, selector, index=0):
        find = selector[3:] if selector.startswith("js:") else f"[...document.querySelectorAll({json.dumps(selector)})][{index}]"
        r = self.js(f"""(()=>{{const e={find};
            if(!e) return null; const b=e.getBoundingClientRect(); return [b.left+b.width/2, b.top+b.height/2, b.top, b.bottom]}})()""")
        if r is None:
            raise RuntimeError(f"no element {selector}[{index}]")
        return r

    def move(self, x, y, steps=6):
        x0, y0 = self.cur
        for i in range(1, steps + 1):
            self.place(x0 + (x - x0) * i / steps, y0 + (y - y0) * i / steps)
            self.frame()

    def click_at(self, x, y):
        for t in ("mousePressed", "mouseReleased"):
            self.cmd("Input.dispatchMouseEvent", type=t, x=x, y=y, button="left", clickCount=1)

    def click(self, selector, index=0, steps=6, then_hold=2, settle=0.9, nav=None):
        """Move the cursor to the element (scrolling it into view), click, and record the result."""
        x, y, top, bottom = self.center(selector, index)
        if top < 0 or bottom > H - 40:
            self.scroll_by(top - H * 0.4)
            x, y, _, _ = self.center(selector, index)
        self.move(x, y, steps)
        self.frame(2)
        self.js("window.__old = 1")
        self.click_at(x, y)
        end = time.time() + (5 if nav else 1.5 if nav is None else 0)
        moved = False
        while time.time() < end:
            time.sleep(0.1)
            try:
                if self.js("window.__old") is None and self.js("document.readyState") == "complete":
                    moved = True
                    break
            except RuntimeError:
                pass
        time.sleep(settle)
        if moved:
            self.js("window.scrollTo(0, 0)")
            self.js(CURSOR_JS)
            self.place(x, y)
        self.snapshot()
        self.frame(then_hold)

    def scroll_by(self, dy, steps=None):
        steps = steps or max(4, min(18, int(abs(dy) / 70)))
        y0 = self.js("scrollY")
        for i in range(1, steps + 1):
            ease = 0.5 - 0.5 * math.cos(math.pi * i / steps)
            self.js(f"window.scrollTo(0,{y0 + dy * ease})")
            self.frame()

    def scroll_to(self, selector, offset=80, steps=None):
        find = selector[3:] if selector.startswith("js:") else f"document.querySelector({json.dumps(selector)})"
        top = self.js(f"{find}.getBoundingClientRect().top")
        self.scroll_by(top - offset, steps)

    def save_gif(self, path, text_path, ms=125):
        """Write the GIF, plus every page state's text so check.sh can scan what the frames show."""
        frames = [f.quantize(colors=128, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE) for f in self.frames]
        frames[0].save(path, save_all=True, append_images=frames[1:], duration=ms, loop=0, optimize=True, disposal=1)
        with open(text_path, "w") as f:
            f.write("\n\n=====\n".join(self.texts))
        self.frames, self.texts = [], []

    def close(self):
        try:
            self.ws.close()
        finally:
            self.proc.terminate()

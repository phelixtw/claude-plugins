"""Record the README dashboard GIFs from the synthetic-data dashboard on :8797.

usage: python record.py <workdir> <gif dir> [name ...]
"""
import os, shutil, sys, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from cdp import Browser

BASE = "http://127.0.0.1:8797"
OUT = sys.argv[2]
H2 = "js:[...document.querySelectorAll('h2')].find(e=>e.textContent.includes({!r}))"


def h2(text):
    return H2.format(text)


def row_starting(prefix):
    return f"js:[...document.querySelectorAll('tr[onclick]')].find(e=>e.textContent.trim().startsWith({prefix!r}))"


def overview(b):
    b.goto(BASE + "/")
    b.hold(12)
    b.scroll_by(430)
    b.hold(14)
    b.scroll_to(h2("每日一覽"), offset=90)
    b.hold(10)
    b.click('.mon[data-m="2026-10"] .mh', then_hold=8, nav=False)
    b.scroll_by(420)
    b.hold(14)
    b.scroll_by(380)
    b.hold(10)


def pages(b):
    b.goto(BASE + "/")
    b.hold(8)
    for href in ["/context", "/tools", "/delegate", "/model", "/expensive", "/optimize", "/"]:
        b.click(f'nav a[href="{href}"]', steps=4, then_hold=8, settle=1.2, nav=True)
    b.hold(4)


def topic(b):
    b.goto(BASE + "/")
    b.hold(8)
    b.click('a.find[href="/context"]', then_hold=10, settle=1.5, nav=True)
    b.scroll_to(h2("哪些對話最貴"), offset=90)
    b.hold(10)
    b.click("js:" + f"{h2('哪些對話最貴')[3:]}.nextElementSibling.querySelector('tbody tr')", then_hold=10, settle=1.5, nav=True)
    b.scroll_to(h2("逐回合"), offset=60)
    b.hold(30)


def drill(b):
    b.goto(BASE + "/")
    b.hold(4)
    b.scroll_to(h2("每日一覽"), offset=90, steps=8)
    b.click('.mon[data-m="2026-10"] .mh', steps=4, then_hold=2, nav=False)
    priciest_day = ("js:[...document.querySelectorAll('tr[onclick*=\"day=2026-10\"]')]"
                    ".sort((a,c)=>c.querySelector('td.num').dataset.v-a.querySelector('td.num').dataset.v)[0]")
    b.click(priciest_day, steps=5, then_hold=6, settle=1.5, nav=True)
    b.click('tr[onclick*="session="]', steps=5, then_hold=6, settle=1.5, nav=True)
    b.scroll_to(h2("逐回合"), offset=60, steps=8)
    b.hold(3)
    priciest_turn = ("js:[...document.querySelectorAll('tr[onclick*=\"/pipeline\"]')]"
                     ".sort((a,c)=>parseFloat(c.cells[2].textContent.slice(1))-parseFloat(a.cells[2].textContent.slice(1)))[0]")
    b.click(priciest_turn, steps=5, then_hold=8, settle=1.5, nav=True)
    b.scroll_to("#ctxw", offset=60, steps=8)
    b.hold(6)
    b.click('label[for="ct-cnt"]', steps=4, then_hold=8, nav=False)
    b.click('label[for="ct-api"]', steps=4, then_hold=10, nav=False)


def verify(b):
    b.goto(BASE + "/verify")
    b.hold(12)
    b.scroll_to(h2("每天的每回合花費"), offset=70)
    b.hold(6)
    days = b.js("""(()=>{const h=[...document.querySelectorAll('rect.hit')].filter(e=>!/ 0 個回合/.test(e.dataset.pop));
        const d=e=>e.dataset.go.match(/since=([\\d-]+)/)[1]; const on=document.querySelector('rect.hit.on');
        return [d(h[Math.floor(h.length*0.3)]), d(h[Math.floor(h.length*0.55)]), on ? d(on) : d(h[2])]})()""")
    for day in days:
        b.click(f'rect.hit[data-go*="since={day}"]', steps=6, then_hold=10, settle=1.6, nav=False)
    b.scroll_to(h2("每一項改了多少"), offset=70)
    b.hold(24)


ALL = {"overview": overview, "pages": pages, "topic": topic, "drill": drill, "verify": verify}
names = sys.argv[3:] or list(ALL)
for n in names:
    profile = os.path.join(sys.argv[1], "chrome-profile-" + n)
    shutil.rmtree(profile, ignore_errors=True)
    b = Browser(profile)
    try:
        ALL[n](b)
        print(n, len(b.frames), "frames")
        b.save_gif(os.path.join(OUT, n + ".gif"), os.path.join(sys.argv[1], n + ".txt"))
    finally:
        b.close()
        time.sleep(1)

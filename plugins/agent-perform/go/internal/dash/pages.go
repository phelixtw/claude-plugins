package dash

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/refs"
	"agentperform/internal/transcript"
)

// dash_pages.py: the /how and /levers pages.

var pollGateRe = pyre.Compile(`agent-perform[:：] ?前景輪詢`)

var (
	gateMu    sync.Mutex
	gateCache = map[string]gateHit{}
)

type gateHit struct {
	mtime, size int64
	n           int
}

// pollGateBlocks is how many transcript lines carry the poll gate's message.
func pollGateBlocks() int {
	loadCaches()
	n := 0
	for _, d := range listdir(projects()) {
		dir := filepath.Join(projects(), d)
		for _, f := range listdir(dir) {
			if !strings.HasSuffix(f, ".jsonl") {
				continue
			}
			p := filepath.Join(dir, f)
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			gateMu.Lock()
			hit, ok := gateCache[p]
			gateMu.Unlock()
			if !ok || hit.mtime != st.ModTime().UnixNano() || hit.size != st.Size() {
				hit = gateHit{st.ModTime().UnixNano(), st.Size(), 0}
				for line := range transcript.RawLines(p) {
					line = strings.ToValidUTF8(line, "")
					if strings.Contains(line, "前景輪詢") && pollGateRe.Has(line) {
						hit.n++
					}
				}
				gateMu.Lock()
				gateCache[p] = hit
				gateMu.Unlock()
				parseDirty.Store(true)
			}
			n += hit.n
		}
	}
	return n
}

// pollGateCard is the foreground-poll gate: how many were blocked and the flow that blocks them.
func pollGateCard(blocked int) string {
	arrow := `<div class="farr">→</div>`
	gates := box("Claude 要跑 Bash 指令", "Bash 工具被呼叫", "PreToolUse") + arrow +
		box("是不是前景輪詢", "sleep / until / while-sleep 迴圈，不是背景執行", "") + arrow +
		box("擋下", "每次檢查都是一次 API 呼叫，會重送整份 context；Claude 改用 run_in_background", "")
	return `<div class="hero tight">` + stats([]stat{{"前景輪詢被擋", py.Comma(int64(blocked)) + " 次", "", "", ""}}) + `</div><div class="flow">` + gates + `</div>`
}

func box(title, sub, tag string) string {
	chip := ""
	if tag != "" {
		chip = `<em>` + Esc(tag) + `</em>`
	}
	return `<div class="fbox"><b>` + Esc(title) + `</b>` + chip + `<span>` + sub + `</span></div>`
}

var priceDayRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// HowView is the /how page.
func HowView(sessions []*Session) string {
	arrow := `<div class="farr">→</div>`
	end := box("對話結束、/clear 或 /resume 切換", "SessionEnd hook", "每個對話") + arrow +
		box("整理這個對話", "委派花費、重複呼叫、token 模型", "")
	parts := [][5]string{
		{"每個回合結束時", "bin/ap ctx-probe", "Stop hook", "不用錢",
			"設定有變或滿一天時重抓 /context 的分類。"},
		{"對話結束、/clear 或 /resume 切換時", "bin/ap session-end", "SessionEnd hook", "不用錢",
			"配對委派花費、統計重複呼叫、更新 token 模型。全是本機 script，不打 API。"},
		{"你打開這個網頁的時候", "bin/ap dashboard", "隨時", "不用錢",
			"讀出上面這些 script 寫下的檔案，算成數字、畫成圖。"},
	}
	var who strings.Builder
	for _, p := range parts {
		who.WriteString(`<div class="gl"><b>` + Esc(p[0]) + ` <span class="cls">` + Esc(p[3]) + `</span></b>` +
			`<span><code>` + Esc(p[1]) + `</code> · ` + Esc(p[2]) + `<br>` + Esc(p[4]) + `</span></div>`)
	}
	files := [][2]string{
		{"subagent_costs.json", "每次委派的 subagent 花費、模型、耗時。"},
		{"nightly.log", "每次 SessionEnd 整理的開始時間，哪一步失敗也記在這裡；成功的步驟不另外記。"},
	}
	var store strings.Builder
	for _, f := range files {
		store.WriteString(`<div class="gl col"><b><code>` + Esc(f[0]) + `</code></b><span>` + Esc(f[1]) + `</span></div>`)
	}
	blocked := pollGateBlocks()
	var vocab strings.Builder
	vocab.WriteString(`<section class="card vocab">`)
	for _, g := range terms {
		vocab.WriteString(`<h4>` + Esc(g.group) + `</h4>`)
		for _, it := range g.items {
			vocab.WriteString(`<div class="gl" id="` + Esc(strings.Join(py.Split(it.term), "-")) + `"><b>` + Esc(it.term) + `</b><span>` + it.body + `</span></div>`)
		}
	}
	vocab.WriteString(`</section>`)
	var items []refs.Item
	for _, k := range refs.Keys() {
		items = append(items, refs.Item{Element: refs.Short(k), Keys: []string{k}})
	}
	items = append(items, refs.Item{Element: "自訂門檻", Reason: "做太久被中止：" + strconv.Itoa(slowKillSec) + " 秒到 " + strconv.Itoa(slowKillMax/60) + " 分鐘；" +
		"重複呼叫：只跟前 3 個回合比對、佔比 15% 才顯示；條件列 30% 的出處是 Thariq、自動 compact 60% 的出處是 Dex Horthy，兩條都列在上方。"})
	srcSec := refs.Section(items)
	body := `<p class="lede"><b>全站數字都來自本機 transcript，前景輪詢已被擋 ` + py.Comma(int64(blocked)) + ` 次。</b>` +
		`每個數字都是規則算的。</p>` +
		`<p class="lede">畫面上的金額是依 API 牌價換算的估計值，訂閱方案並不是這樣計費。` + prices.Source() + `。</p>` +
		`<p class="lede"><a href="#flow">流程</a> · <a href="#hooks">hook</a> · ` +
		`<a href="#terms">名詞</a> · <a href="#sources">出處</a></p>` +
		`<div class="sec" id="flow">每個對話結束</div>` +
		`<section class="card"><div class="flow">` + end + `</div></section>` +
		`<div class="sec">各自在什麼時候動</div>` +
		`<section class="card">` + who.String() + `</section>` +
		`<div class="sec">各自把資料寫在哪</div>` +
		`<section class="card">` + store.String() + `</section>` +
		`<div class="sec" id="hooks">擋下前景輪詢</div>` +
		`<section class="card">` + pollGateCard(blocked) + `</section>` +
		`<div class="sec" id="terms">名詞定義</div>` + vocab.String() +
		`<div class="sec" id="sources">` + refs.Title + `</div>` + srcSec
	priceDay, priceSub := "內建表", "官方價目頁抓取失敗或尚未更新"
	if d := priceDayRe.FindString(prices.Source()); d != "" {
		priceDay, priceSub = d[5:], "Anthropic 官方價目頁"
	}
	tiles := stats([]stat{{"個對話", py.Comma(int64(len(sessions))), "讀自本機 transcript", "", ""},
		{"價目更新", priceDay, priceSub, "", ""}})
	return `<h1 class="view">怎麼算</h1>` +
		`<p class="lede">這頁只放說明：每個數字怎麼算、資料從哪來。數字本身都在各自的頁面。</p>` +
		`<div class="hero">` + tiles + `</div>` +
		`<h2 class="nh">每個數字怎麼來</h2>` + body
}

var _ = sort.Strings

// Code generated from scripts/dash_views.py string literals.

package dash

const costTableHowA = "<p>主對話每次 API 呼叫都要重讀整個 context，探索過程留在 context 裡，之後每次 API 呼叫都要再付一次。</p><p>委派是讓 subagent 去探索，只把結論帶回主對話。省下和多花的分別這樣算，代入的是表上這一列的一般值：</p>"
const costTableHowB = "<p>Opus 5.5 的價目：cache 讀 $0.20/M、cache 寫（1 小時）$8/M。價目表在 <code>go/internal/prices</code>，用實際帳單驗算過到小數第六位。</p>"
const costTableCols = "<ul><li><b>委派時 context</b>：委派當下主對話的 context 大小。主對話之後每次 API 呼叫都要重讀這麼多。數字越大，委派省得越多。</li><li><b>之後主對話 API 呼叫數</b>：委派之後主對話又做了幾次 API 呼叫。次數越多，委派省得越多。</li><li><b>subagent API 呼叫數</b>：subagent 自己做了幾次 API 呼叫。subagent 每次也要重讀自己的 context，花費隨次數平方成長。</li><li><b>cache 寫入</b>：subagent 探索過程寫進 subagent context 的量，不含開頭的前綴。沒有委派的話，這些內容會留在主對話的 context。</li><li><b>回傳結論</b>：subagent 回傳進主對話的結論大小，已扣掉你在同一個回合打的字（兩者算在同一筆 cache 寫入）。cache 寫入減掉回傳結論，就是沒進主對話 context 的量。</li><li><b>委派實際花費</b>：這段期間所有委派的 subagent 實際花費合計。</li></ul><p>中間五欄是<b>一般值</b>（由小排到大正中間那筆），避免單次花費特別高的委派拉高平均。</p>"
const diagnoseNoteA = "「浪費」＝ context 超過視窗 40% 的回合，每回合花費減掉 150–300k 回合的一般花費（"
const diagnoseNoteB = "／回合）後加總，再加上做太久被中止的回合的花費，以及 Cache 過期重寫多花的金額。這是上限：context 長的回合通常工作也比較複雜，價差不全來自 context 長度。適合用來排優先順序，實際省得下來的金額會比較少。"
const tagHelp = "<div class=\"sec\"><details class=\"ex\"><summary>Tag 欄是什麼</summary><p>這個回合踩到的問題：做太久被中止、重複呼叫、碰到 Claude Code 的工具上限（Read、Bash、Edit、Glob、WebFetch、PDF，滑鼠移上去看上限），Cache 過期重寫佔花費 10% 以上。context 大小與 Cache hit 點進回合看。</p></details></div>"
const exampleNote = "</pre><p>{r}×{C}：subagent 的 {r} 次 API 呼叫若在主對話做，每次都要讀主對話的 context {C}。{g}×{tri}：每多一次 API 呼叫 context 增加 {g}，累計 0+1+…+{n}={tri} 份。這裡用一般值代入公式；表上「每次淨省」是每次委派各自算完再取一般值，所以兩個數字不同。</p>"
const noDelegate = "還沒有可推估的委派紀錄。每個對話結束時會計算，沒算到的由下一個對話結束時補上。"

const (
	expensiveTitle   = "最貴的回合"
	expensiveLede    = "跨所有對話，依你打的每一句話引發的全部 token 排序，含這句話啟動的 subagent。背景工作通知與 /compact 後的接續回合已併入發起它的那句話。"
	expensiveNote    = "token ＝ 主對話與 subagent 所有 API 呼叫的輸入、輸出、cache 讀寫加總。佔比是佔近期全部 token（主對話加 subagent）的比例。排行只列這句話本身有 API 呼叫的回合。"
	expensiveEmpty   = "近期沒有可排行的回合。"
	expensiveHead    = "你打的字"
	subagentCard     = "依 subagent 類型"
	subagentNote     = "次數、token 與花費都取自每次委派的 transcript。token 佔比是佔近期全部 token（主對話加 subagent）的比例；沒有配對到 transcript 的委派只算次數。"
	activeStatLabel  = "實際作業時數"
	activeStatTip    = "相鄰兩筆紀錄間隔小於 5 分鐘才累加；掛著的時間是每個對話從第一筆到最後一筆。"
	tokenStatLabel   = "總 token"
	delegateStatName = "委派 token"
)

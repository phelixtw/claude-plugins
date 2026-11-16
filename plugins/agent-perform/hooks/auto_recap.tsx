import type { EngineInterface, Register } from 'claude-code'
import { Activity, checkActivity, classicActivity, closeActivity, drawActivity, drawHistory, focusActivity, HISTORY, stepActivity, turnEndActivity, turnStartActivity } from './activity'
import { hl, marqueeRow, redraw, STEP_MS, width, type Cell, type Pager, type Tone } from './cells'

// After a turn ends:
// - context 30% or more: add a row to the mod above the prompt offering /clear, /compact or /recap
// - context >= 60%, idle 1 h, option autoCompact on: compact with the recap, no question
const AUTO_IDLE_MS = 60 * 60_000
const ASK_LOW = 30
const AUTO_MIN = 60

const CLEAR = '/clear'
const RECAP = '/recap'
const COMPACT = '/compact'
// Divider over the mod, cut to the terminal width.
const RULE = '─'.repeat(400)
const DASHBOARD = 'http://localhost:8787/?session='
const PIPELINE = 'http://localhost:8787/pipeline?session='
const TOPIC = 'http://localhost:8787/'

// Stats lines come from the statusline script's --mod mode; always shown.
let stats: string[] = []
let statsSid = ''
let cwd = ''
let refreshing = false
// Suggestion row: shown when a turn ends inside the context window, with the percent it saw.
let modPercent: number | null = null

let timer: { cancel(): void } | null = null

// Page shown per marquee row, by row name.
const pageOf: Record<string, number> = {}
// Items past a threshold, named by the scorecard script (ctx, cache, burst); the burst is held back from the next prompt until its turn ends.
let alertNames: string[] = []
let alertsHeld = false

// 5-cell gauge, rounded up so any usage shows a cell.
function gauge(pct: number): string {
  const n = Math.max(0, Math.min(5, Math.ceil(pct / 20)))
  return '▰'.repeat(n) + '▱'.repeat(5 - n)
}

// Plugin version for the mod's top-right corner, read once from plugin.json.
let version = ''

async function loadVersion($: EngineInterface): Promise<void> {
  if (version) return
  const res = await $.process.run(['cat', `${$.plugin.root}/.claude-plugin/plugin.json`], { timeoutMs: 2000 })
  const v = res.exitCode === 0 ? JSON.parse(res.stdout).version : ''
  if (typeof v === 'string' && v) {
    version = v
    redraw($)
  }
}

// Model/effort picker opened from the model name in row 1.
const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max']
let menu: { models: string[]; current: string } | null = null
let effort = ''

async function toggleMenu($: EngineInterface): Promise<void> {
  if (menu) {
    menu = null
  } else {
    const row = (await $.config.list()).find(r => r.key === 'model')
    menu = { models: row?.options ?? [], current: String(row?.value ?? '') }
  }
  await $.ui.invalidate('ui.render')
}

async function pickModel($: EngineInterface, value: string): Promise<void> {
  menu = null
  await $.config.set({ key: 'model', value })
  lastRun = -Infinity
  await refreshStats($)
}

async function pickEffort($: EngineInterface, value: string): Promise<void> {
  menu = null
  effort = value
  await $.command.run({ command: 'effort', args: value })
  await $.ui.invalidate('ui.render')
}

function stop() {
  timer?.cancel()
  timer = null
}

// Compacts with the recap as the instructions; plain compact if the recap came back empty.
async function compactWithRecap($: EngineInterface) {
  const { text } = await $.command.run({ command: 'recap', args: '' })
  await $.session.compact(text?.trim() ? { instructions: text.trim() } : {})
}

type Limit = { pct: number; resetAt?: number }

const REFRESH_MS = 30_000

// Statusline row 1 pieces; elapsed and quota countdowns are recomputed from absolute times each second.
let row1: string[] = []
let startMs: number | null = null
// Absolute time the prompt cache lapses, so the cell ticks between script runs.
let cacheEndMs: number | null = null
let limits: Record<string, Limit> = {}
let lastRun = -Infinity
let trailing: { cancel(): void } | null = null
let ticking: { cancel(): void } | null = null

function setLimits(list: ReadonlyArray<{ kind: string; percentUsed: number; resetsAt?: string }>) {
  limits = {}
  for (const l of list) limits[l.kind] = { pct: l.percentUsed, resetAt: l.resetsAt ? Date.parse(l.resetsAt) : undefined }
}

// d/h past a day (countdowns only), h/m above an hour, m/s below it so the tick is visible.
function fmt(ms: number, days = true): string {
  const t = Math.floor(ms / 1000)
  if (days && t >= 86_400) return `${Math.floor(t / 86_400)}d${Math.floor((t % 86_400) / 3600)}h`
  if (t >= 3600) return `${Math.floor(t / 3600)}h${String(Math.floor((t % 3600) / 60)).padStart(2, '0')}m`
  return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, '0')}s`
}

function quota(kind: string, fallback: string, now: number): string {
  const l = limits[kind]
  if (!l) return fallback
  const used = `已用 ${Math.round(l.pct)}%`
  if (l.resetAt === undefined) return used
  return l.resetAt <= now ? `${used}(已重置)` : `${used}(${fmt(l.resetAt - now)})`
}

// Builds row 1 from the script's text, swapping in the live elapsed time and countdowns.
function liveRow1(now: number): string[] {
  const p = [...row1]
  const m = /^(?:(\d+)h)?(\d+)m$/.exec(p[2] ?? '')
  if (m) {
    const mins = Number(m[1] ?? 0) * 60 + Number(m[2])
    if (startMs === null || Math.abs((now - startMs) / 60_000 - mins) >= 2) startMs = now - mins * 60_000
    p[2] = fmt(now - startMs, false)
  }
  const tag = /\/(1h|5m)$/.exec(p[2] ?? '')
  if (cacheEndMs !== null && tag) {
    p[2] = cacheEndMs > now ? `Cache 剩 ${fmt(cacheEndMs - now, false)}/${tag[1]}` : 'Cache 已過期'
  }
  const five = /^5h (.*)$/.exec(p[4] ?? '')
  const seven = /^7d (.*)$/.exec(p[5] ?? '')
  if (five) p[4] = `5h ${quota('five_hour', five[1], now)}`
  if (seven) p[5] = `7d ${quota('seven_day', seven[1], now)}`
  return p
}

// Row 2 from the scorecard: the session line's turn and burst, row 1's context and Cache, the subagent line.
// Cost and waste stay on the dashboard.
type Row2 = { turn: string | null; ctx: string | null; burst: string | null; cache: { text: string; isExpired: boolean } | null; delegate: string | null }

function parseRow2(lines: readonly string[], p: readonly string[], sid8: string): Row2 {
  const sc = (sid8 && lines.find(l => l.startsWith(sid8))) || ''
  const burst = sc.split(' · ').find(x => x.startsWith('本回合 context'))
  const pct = /^context 已用 (\d+)%$/.exec(p[3] ?? '')
  const cache = /^Cache (?:剩 )?(.*)$/.exec(p[2] ?? '')
  // A subagent line may carry " · note"; inside an item it becomes a parenthetical.
  const launched = lines.find(l => l.startsWith('本回合') && !l.startsWith('本回合 context'))
  return {
    turn: /^\S+ #(\d+)/.exec(sc)?.[1] ?? null,
    ctx: pct ? `${gauge(Number(pct[1]))} ${pct[1]}%` : p[3] === 'context 已用 --' ? `${gauge(0)} --` : (p[3] ?? null),
    burst: burst ? /^本回合 context (\S+?)(?:（|$)/.exec(burst)?.[1] ?? null : null,
    cache: cache ? { text: `◷ ${cache[1]}`, isExpired: p[2] === 'Cache 已過期' } : null,
    delegate: launched?.replace(/ · (.*)$/, '（$1）').replace(/^本回合/, '') ?? null,
  }
}

// Runs the scorecard script for the three rows (at most once per 30 s) and marks the session so the statusline stays empty.
async function refreshStats($: EngineInterface): Promise<void> {
  const now = await $.clock.now()
  if (refreshing) return
  if (now - lastRun < REFRESH_MS) {
    // Throttled: run once more when the window ends.
    if (!trailing) {
      trailing = $.clock.after(REFRESH_MS - (now - lastRun), () => {
        trailing = null
        void refreshStats($).catch(() => {})
      })
    }
    return
  }
  refreshing = true
  lastRun = now
  try {
    await runStats($)
  } finally {
    refreshing = false
  }
}

function ensureTimers($: EngineInterface) {
  if (ticking) return
  const tick = $.clock.every(STEP_MS, () => redraw($))
  const poll = $.clock.every(REFRESH_MS, () => void refreshStats($).catch(() => {}))
  ticking = { cancel: () => (tick.cancel(), poll.cancel()) }
}

async function runStats($: EngineInterface): Promise<void> {
  const sid = await $.session.id()
  const usage = await $.session.usage()
  setLimits(usage.rateLimits)
  const rl: Record<string, { used_percentage: number; resets_at?: number }> = {}
  for (const [kind, l] of Object.entries(limits)) {
    rl[kind] = { used_percentage: l.pct, resets_at: l.resetAt === undefined ? undefined : l.resetAt / 1000 }
  }
  const pct = usage.context.percent
  cwd = await $.session.cwd()
  // Until a request reports it, take the effort from settings, so a reload doesn't blank it.
  if (!effort) {
    const lvl = (await $.settings.read().catch(() => ({}))).effortLevel
    if (typeof lvl === 'string') effort = lvl
  }
  const input = JSON.stringify({
    session_id: sid,
    model: { display_name: await $.session.model() },
    workspace: { current_dir: cwd },
    context_window: { context_window_size: usage.context.window, remaining_percentage: pct === undefined ? null : 100 - pct },
    rate_limits: rl,
  })
  // Rejects when bin/ap cannot start (the plugin's cache dir was replaced under a running session) or times out.
  const res = await $.process.run([`${$.plugin.root}/bin/ap`, 'scorecard', '--mod'], { stdin: input, timeoutMs: 8000 }).catch(() => null)
  // eslint-disable-next-line no-control-regex
  const all = (res?.stdout ?? '').replace(/\x1b\[[0-9;]*m/g, '').split('\n').filter(l => l.trim())
  const flagged = all.find(l => l.startsWith('alert:'))
  const lines = all.filter(l => l !== flagged)
  if (!res || res.exitCode !== 0 || lines.length === 0) {
    // A stale countdown would run out and show a false expiry.
    if (cacheEndMs !== null && /^Cache /.test(row1[2] ?? '')) row1[2] = 'Cache --'
    cacheEndMs = null
    return
  }
  row1 = lines[0].split(' · ')
  cacheEndMs = null
  const c = /^Cache 剩 (?:(\d+)h(\d+)m|(\d+)m(\d+)s)\//.exec(row1[2] ?? '')
  if (c) {
    const secs = c[1] !== undefined ? Number(c[1]) * 3600 + Number(c[2]) * 60 : Number(c[3]) * 60 + Number(c[4])
    cacheEndMs = (await $.clock.now()) + secs * 1000
  }
  stats = lines.slice(1)
  const fresh = flagged ? flagged.slice(6).split(',') : []
  if (fresh.some(n => !alertNames.includes(n))) for (const k of Object.keys(pageOf)) pageOf[k] = 0
  alertNames = fresh
  statsSid = sid
  await $.fs.write(`/tmp/claude-ap-mod-${sid}`, '1').catch(() => {})
  await $.ui.invalidate('ui.render')
}

async function setMod($: EngineInterface, pct: number | null): Promise<void> {
  if (modPercent === pct) return
  modPercent = pct
  await $.ui.invalidate('ui.render')
}

// The hotkey row follows the context fill itself, not the turn: shown from ASK_LOW up,
// and after ✕ it stays away until the fill grows another DISMISS_STEP points.
const DISMISS_STEP = 10
let dismissedAt: number | null = null
async function syncMod($: EngineInterface): Promise<void> {
  const { context } = await $.session.usage()
  const pct = context.percent ?? 0
  if (pct < ASK_LOW) {
    dismissedAt = null
    return setMod($, null)
  }
  if (dismissedAt !== null && pct < dismissedAt + DISMISS_STEP) return setMod($, null)
  dismissedAt = null
  return setMod($, Math.round(pct))
}

// Hides the mod, then runs the chosen action.
async function choose($: EngineInterface, action: () => Promise<unknown>): Promise<void> {
  await setMod($, null)
  await action().catch(() => {})
}

const opener = ($: EngineInterface) => (url: string) => void $.process.run(['open', url]).catch(() => {})

export const register: Register = (on, options) => {
  const autoCompact = options.autoCompact === true
  const log = new Activity()

  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    await turnEndActivity($, log, e.agentId)
    stop()
    alertsHeld = false
    lastRun = -Infinity
    void refreshStats($).catch(() => {})
    ensureTimers($)
    await syncMod($)
    const { context } = await $.session.usage()
    const pct = context.percent ?? 0
    if (autoCompact && pct >= AUTO_MIN) {
      timer = $.clock.after(AUTO_IDLE_MS, () => {
        timer = null
        void compactWithRecap($).catch(() => {})
      })
    }
    return result
  })

  on('turn.start', async ($, e, next) => {
    const result = await next(e)
    await turnStartActivity($, log, e.agentId)
    return result
  })

  // Remembers the effort the requests go out with, to show beside the model.
  on('turn.step', async function* ($, e, next) {
    if (typeof e.effort === 'string') effort = e.effort
    return yield* stepActivity($, log, e, next)
  })

  // Observers: a failure here passes the event on as it was.
  on('tool.check', async ($, e, next) => {
    const verdict = await next(e)
    await checkActivity($, log, e, verdict.decision)
    return verdict
  }).catch(($, e, next) => next(e))

  on('classic.*', ($, e, next) => classicActivity($, log, next.event.slice('classic.'.length), e, next)).catch(($, e, next) => next(e))

  on('session.end', async ($, e, next) => {
    log.reset()
    redraw($)
    return next(e)
  })

  on('ui.focus', { component: 'Pane', requestId: HISTORY }, ($, e, next) => focusActivity($, log, e, next)).catch(($, e, next) => next(e))

  on('ui.close', ($, e, next) => closeActivity($, log, e, next))

  // A rate-limit window moved: take the new figures now, redraw, and refresh the rest on the throttle.
  on('session.measure', async ($, e, next) => {
    const result = await next(e)
    ensureTimers($)
    if (e.changed.includes('rateLimits')) {
      setLimits(e.rateLimits)
      redraw($)
    }
    void syncMod($).catch(() => {})
    void refreshStats($).catch(() => {})
    return result
  })

  // Any new prompt means the user is back: drop whatever was pending.
  on('prompt.submit', async ($, e, next) => {
    stop()
    alertsHeld = true
    // The turn row moves to the new turn number right away instead of on the next 30 s poll.
    $.clock.after(1500, () => {
      lastRun = -Infinity
      void refreshStats($).catch(() => {})
    })
    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: HISTORY }, async ($, e) => {
    const shortId = statsSid.slice(0, 8)
    const links = { session: shortId ? `${DASHBOARD}${shortId}` : '', pipeline: shortId ? `${PIPELINE}${shortId}` : '' }
    const columns = e.props.bodyColumns ?? (e.viewport?.columns ?? 80)
    return drawHistory($, log, { el: $.ui.resolve(e), now: await $.clock.now(), columns, rows: e.props.scroll.bodyRows, links, cwd, open: opener($) })
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    ensureTimers($)
    if (stats.length === 0) void refreshStats($).catch(() => {})
    if (!version) void loadVersion($).catch(() => {})
    const now = await $.clock.now()
    const el = $.ui.resolve(e)
    const { Box, Button, Text } = el
    const shortId = statsSid.slice(0, 8)
    const open = opener($)
    const go = (url: string) => (url ? () => open(url) : undefined)
    const columns = (e.props.bodyColumns ?? (e.viewport?.columns ?? 80) - 5) - 2
    const below = drawActivity($, log, { el, now, columns, screenRows: e.viewport?.rows, isWorking: e.props.isWorking, cwd })
    if (row1.length === 0 && stats.length === 0 && modPercent === null && below.length === 0) return next(e)
    const pager = (name: string): Pager => ({ page: pageOf[name] ?? 0, flip: page => { pageOf[name] = page; redraw($) } })
    const p = liveRow1(now)
    const r2 = parseRow2(stats, p, shortId)
    const dash = shortId ? `${DASHBOARD}${shortId}` : ''
    const turnUrl = r2.turn ? `${PIPELINE}${shortId}&turn=${r2.turn}` : ''
    // Context and Cache are standing state and stay lit; only the per-turn burst waits for its turn to end.
    const alert = (name: string) => alertNames.includes(name) && !(alertsHeld && name === 'burst')
    const quota = (idx: number) => /^\w+ .*$/.exec(p[idx] ?? '')?.[0]
    const scroll = (name: string, text: string | undefined, f?: () => void, tone: Tone = 'plain'): Cell[] => (text ? [{ kind: 'scroll', name, text, tone, go: f }] : [])
    const row1Cells = [
      ...scroll('model', p[0] && (effort ? `${p[0]} ${effort}` : p[0]), () => void toggleMenu($).catch(() => {})),
      ...scroll('cwd', cwd ? p[1] : undefined, () => open(cwd)),
      ...scroll('5h', quota(4)),
      ...scroll('7d', quota(5)),
    ]
    const ctx: Cell[] = r2.ctx === null ? [] : [{
      kind: 'rigid',
      name: 'ctx',
      parts: [
        { text: r2.ctx, tone: alert('ctx') ? 'warning' : 'plain' },
        ...(r2.burst ? [{ text: ' ', tone: 'plain' as Tone }, { text: r2.burst, tone: (alert('burst') ? 'warning' : 'plain') as Tone }] : []),
      ],
      go: go(turnUrl),
    }]
    const row2Cells = [
      ...ctx,
      ...scroll('cache', r2.cache?.text, go(`${TOPIC}context?focus=cachebreak`), r2.cache?.isExpired ? 'dire' : alert('cache') ? 'warning' : 'plain'),
      ...scroll('delegate', r2.delegate ?? undefined, go(`${TOPIC}delegate`)),
    ]
    // The session id opens its dashboard page, `› #N` that turn's pipeline page.
    const turnLabel = r2.turn ? `› #${r2.turn}` : ''
    const label = shortId ? [
      <Button key="ar-sid" hover={hl('ar-sid')} plain onPress={() => open(dash)}>{shortId}</Button>,
      turnLabel ? <Text key="ar-sid-gap"> </Text> : null,
      turnLabel ? <Button key="ar-turn" hover={hl('ar-turn')} plain onPress={() => open(turnUrl)}>{turnLabel}</Button> : null,
      <Text key="ar-label-end">：</Text>,
    ] : []
    const labelWidth = shortId ? width(shortId) + (turnLabel ? 1 + width(turnLabel) : 0) + 2 : 0
    const marquees = [
      marqueeRow(el, { name: 'r1', cells: row1Cells, avail: columns, now, pager: pager('r1') }),
      marqueeRow(el, { name: 'r2', label, labelWidth, cells: row2Cells, avail: columns, now, pager: pager('r2') }),
    ]
    const pick = (kind: string, v: string, current: boolean, press: () => Promise<void>) => current
      ? <Text key={`ar-${kind}-${v}`} color="claude" bold>{`● ${v}`}</Text>
      : <Button key={`ar-${kind}-${v}`} hover={hl(`ar-${kind}-${v}`)} plain label={v} onPress={() => void press().catch(() => {})} />
    const picker = !menu ? null : (
      <Box key="ar-menu" flexDirection="column" backgroundColor="userMessageBackground" paddingX={1} alignSelf="flex-start">
        <Box key="ar-menu-model" flexDirection="row" flexWrap="wrap" columnGap={1}>
          <Text color="suggestion" bold>model </Text>
          {menu.models.map(v => pick('model', v, v === menu!.current, () => pickModel($, v)))}
        </Box>
        <Box key="ar-menu-effort" flexDirection="row" flexWrap="wrap" columnGap={1}>
          <Text color="suggestion" bold>effort</Text>
          {EFFORTS.map(v => pick('effort', v, v === effort, () => pickEffort($, v)))}
          <Button key="ar-menu-close" hover={hl("ar-menu-close")} plain label="✕" onPress={() => void toggleMenu($).catch(() => {})} />
        </Box>
      </Box>
    )
    const ask = modPercent === null ? null : (
      <Box flexDirection="row" flexWrap="wrap" columnGap={1}>
        <Button key="ar-clear" hover={hl("ar-clear")} plain label={CLEAR} onPress={() => void choose($, () => $.command.run({ command: 'clear', args: '' }))} />
        <Button key="ar-compact" hover={hl("ar-compact")} plain label={COMPACT} onPress={() => void choose($, () => $.session.compact({}))} />
        <Button key="ar-recap" hover={hl("ar-recap")} plain label={RECAP} onPress={() => void choose($, () => $.command.run({ command: 'recap', args: '' }))} />
        <Button key="ar-close" hover={hl("ar-close")} plain label="✕" onPress={() => { dismissedAt = modPercent; void setMod($, null) }} />
      </Box>
    )
    // Stack over what the rest of the chain drew, two cells in like the other mods.
    return (
      <Box flexDirection="column">
        <Box flexDirection="row">
          <Box flexGrow={1} flexShrink={1} overflow="hidden">
            <Text dimColor wrap="truncate-end">{RULE}</Text>
          </Box>
          <Box flexDirection="row" columnGap={1} marginLeft={1} flexShrink={0}>
            {version ? <Text dimColor>{`v${version}`}</Text> : null}
          </Box>
        </Box>
        <Box flexDirection="column" paddingLeft={2}>
          {picker}
          {marquees}
          {below}
          {ask}
        </Box>
        {await next(e)}
      </Box>
    )
  })
}

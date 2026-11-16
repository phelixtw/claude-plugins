import type { EngineInterface, HookStream, PaneCloseInput, RenderNode, TurnStepChunk, TurnStepInput, TurnStepResult, UiFocusInput, UiFocusResult } from 'claude-code'
import { clip, hl, pad, redraw, width, type Els } from './cells'

// What Claude Code is doing now (the band's row 3) and the session's history of it, as one log.

type Category = '思考' | '回覆' | 'API' | 'Hook' | 'Read' | 'Write' | 'Bash' | 'Agent'
type ToolCategory = 'Read' | 'Write' | 'Bash' | 'Agent'
type Outcome = 'ok' | 'error' | 'denied' | 'interrupted'
type Args = Readonly<Record<string, unknown>>

type LivePhase = 'drafting' | 'pending' | 'asking' | 'running'
type Phase = { name: LivePhase; since: number } | { name: 'done'; at: number; ms: number | null; outcome: Outcome }
// Phases only move forward; asking → running is the one move within a rank.
const RANK: Record<Phase['name'], number> = { drafting: 0, pending: 1, asking: 2, running: 2, done: 3 }

type Entry = {
  seq: number
  // tool_use_id for a tool call; a step's or a hook run's own key otherwise.
  key: string
  kind: 'tool' | 'step' | 'hook'
  cat: Category
  // tool: from its arguments; API: the model; 思考/回覆: the tail streamed so far.
  subject: string
  tool: string
  input: string
  agentId?: string
  turnAt: number
  startedAt: number
  phase: Phase
}

// A hook run under the noise floor: no seq and no history row until it has run HOOK_FLOOR_MS.
type Quiet = { key: string; subject: string; agentId?: string; turnAt: number; since: number }

// What one source says about a tool call. Facts merge in any order; a late or repeated one changes nothing.
type Fact =
  | { kind: 'drafted'; tool: string; agentId?: string }
  | { kind: 'called'; tool: string; args: Args }
  | { kind: 'verdict'; tool: string; decision: 'allow' | 'ask' | 'deny'; agentId?: string }
  | { kind: 'asked' }
  | { kind: 'answered'; isAllowed: boolean }
  | { kind: 'ran'; tool: string; ms?: number; outcome: 'ok' | 'error' | 'interrupted'; agentId?: string }
  | { kind: 'refused' }

// The rows drawn, and which end holds still when the band's room changes.
type View = { isOpen: false } | { isOpen: true; top: number; bottom: number; anchor: 'top' | 'bottom' }
type Target = { tool: string; agent?: string } | { agent: string } | { at: number }

export type Row = {
  seq: number
  cat: Category
  who: string
  subject: string
  state: 'drafting' | 'asking' | 'running' | 'done'
  ms: number | null
  outcome?: Outcome
  startedAt: number
  target: Target
}

type Snapshot = {
  live: { row: Row; more: number } | null
  recent: Row | null
  history: { rows: Row[]; older: number; newer: number } | null
}

// Row 3's time keeps up at ten frames a second while anything runs.
const FRAME_MS = 100
const CAP = 500
const HARD_CAP = 1000
const PAGE = 60
// Rows drawn at once while the history is open, at most: keeps the tree far under its text budget at any width.
const MAX_ROWS = 2 * PAGE
const HOOK_FLOOR_MS = 10
const SNIFF_BYTES = 2048
const TAIL_CHARS = 200
const CAT_W = 5
const SUBJECT_MAX = 100

type ToolRule = { cat: ToolCategory; subject: (a: Args, tool: string) => string; sniff: string }
const str = (v: unknown) => (typeof v === 'string' ? v : '')
const join = (...xs: unknown[]) => xs.map(str).filter(Boolean).join(' ')
const TOOLS: Record<string, ToolRule> = {
  Read: { cat: 'Read', subject: a => str(a.file_path), sniff: 'file_path' },
  Grep: { cat: 'Read', subject: a => join(a.pattern, a.path), sniff: 'pattern' },
  Glob: { cat: 'Read', subject: a => join(a.pattern, a.path), sniff: 'pattern' },
  Write: { cat: 'Write', subject: a => str(a.file_path), sniff: 'file_path' },
  Edit: { cat: 'Write', subject: a => str(a.file_path), sniff: 'file_path' },
  NotebookEdit: { cat: 'Write', subject: a => str(a.notebook_path), sniff: 'notebook_path' },
  Bash: { cat: 'Bash', subject: a => str(a.command), sniff: 'command' },
  Agent: { cat: 'Agent', subject: a => join(a.subagent_type, a.description), sniff: 'description' },
}
// Every other tool (Skill, MCP, Web...) counts as Agent, led by its own name.
const OTHER: ToolRule = { cat: 'Agent', subject: (a, tool) => join(tool, Object.values(a).find(v => typeof v === 'string')), sniff: '' }
const ruleOf = (tool: string) => TOOLS[tool] ?? OTHER
const argsOf = (v: unknown): Args => {
  if (!v || typeof v !== 'object') return {}
  const { tool: _t, tool_use_id: _id, ...rest } = v as Record<string, unknown>
  return rest
}

const BLOCK = { text: '回覆', thinking: '思考' } as const

// The classic events whose command hooks are timed as Hook rows, and what each says about its tool call.
type HookRule = {
  call?: (e: any, log: Activity) => string | undefined
  subject?: (e: any) => string
  enter?: (e: any) => Fact
  leave?: (r: any) => Fact | null
  // Its run belongs to the turn it opens, not the one under way.
  opensTurn?: true
}
const toolId = (e: any) => (typeof e.tool_use_id === 'string' ? e.tool_use_id : undefined)
const HOOKS: Record<string, HookRule> = {
  UserPromptSubmit: { opensTurn: true },
  PreToolUse: {
    call: toolId,
    subject: e => String(e.tool),
    enter: e => ({ kind: 'called', tool: String(e.tool), args: argsOf(e) }),
    leave: r => (r?.deny ? { kind: 'refused' } : null),
  },
  // Raised only when a person is about to be asked, so an ask the mode settles never shows 等待允許.
  PermissionRequest: {
    call: (e, log) => log.asking(String(e.tool_name), e.tool_input, e.agent_id),
    subject: e => str(e.tool_name),
    enter: () => ({ kind: 'asked' }),
    leave: r => (r?.decision ? { kind: 'answered', isAllowed: r.decision.behavior === 'allow' } : null),
  },
  PostToolUse: {
    call: toolId,
    subject: e => str(e.tool_name),
    enter: e => ({ kind: 'ran', tool: str(e.tool_name), ms: e.duration_ms, outcome: 'ok', agentId: e.agent_id }),
  },
  PostToolUseFailure: {
    call: toolId,
    subject: e => str(e.tool_name),
    enter: e => ({ kind: 'ran', tool: str(e.tool_name), ms: e.duration_ms, outcome: e.is_interrupt ? 'interrupted' : 'error', agentId: e.agent_id }),
  },
  PermissionDenied: { call: toolId, subject: e => str(e.tool_name), enter: () => ({ kind: 'refused' }) },
  PostToolBatch: {}, Stop: {}, SubagentStart: {}, SubagentStop: {}, PreCompact: {}, PostCompact: {}, Notification: {}, SessionStart: {},
}

// The first `field` string of a tool call's arguments while they are still being written.
function sniff(json: string, field: string): string | null {
  if (!field) return null
  const m = new RegExp(`"${field}"\\s*:\\s*"((?:[^"\\\\]|\\\\.)*)`).exec(json)
  if (!m) return null
  try {
    return JSON.parse(`"${m[1].replace(/\\$/, '')}"`)
  } catch {
    return m[1]
  }
}

const oneLine = (s: string) => s.replace(/\s+/g, ' ').trim()
// A block's latest line worth reading: a code fence alone says nothing.
const lastLine = (s: string) => s.split('\n').map(l => l.trim()).filter(l => l && !/^(`{3,}|~{3,})\S*$/.test(l)).pop() ?? ''

// Every activity fact lands here, in one environment's single thread, so no two writes interleave.
export class Activity {
  private entries: Entry[] = []
  private byKey = new Map<string, Entry>()
  private open = new Map<string, Entry>()
  private quiet = new Map<string, Quiet>()
  private steps = new Map<string, { agentId?: string; block: string | null; tools: Map<number, string> }>()
  private names = new Map<string, string>()
  private turnAt = 0
  private view: View = { isOpen: false }
  // History rows the band has room for, from the last render; PAGE until one says.
  private fit = PAGE
  private nextSeq = 1
  private nextHook = 1
  // The terminal's rows at the band's last render, which sizes the history pane.
  screenRows = 40
  // The frame timer while busy; paint starts it and it stops itself.
  frames: { cancel(): void } | null = null

  turnStarted(now: number) {
    this.turnAt = now
  }

  isNameless(agentId: string) {
    return !this.names.has(agentId)
  }

  named(agentId: string, name: string) {
    this.names.set(agentId, name ? clip(name, 12) : agentId.slice(0, 6))
  }

  // A new request: the loop's earlier tool calls are over, whatever they reported.
  stepOpened(ref: string, agentId: string | undefined, model: string, now: number) {
    this.sweep(agentId, now)
    this.steps.set(ref, { agentId, block: null, tools: new Map() })
    this.append({ key: `${ref}:api`, kind: 'step', cat: 'API', subject: model, agentId, startedAt: now, phase: { name: 'running', since: now } })
  }

  // Whether `chunk` needs the time: a block or a call starts, or the response ends.
  needsTime(ref: string, c: TurnStepChunk) {
    return c.kind === 'tool' || c.kind === 'stop' || ((c.kind === 'text' || c.kind === 'thinking') && !this.open.has(`${ref}:${c.index}`))
  }

  chunk(ref: string, c: TurnStepChunk, now: number) {
    const st = this.steps.get(ref)
    if (!st || c.kind === 'engine') return
    if (c.kind === 'input') return this.sniffed(st.tools.get(c.index), c.json)
    this.close(`${ref}:api`, 'ok', now)
    if (c.kind === 'text' || c.kind === 'thinking') {
      const key = `${ref}:${c.index}`
      let en = this.open.get(key)
      if (!en) {
        this.closeBlock(st, 'ok', now)
        en = this.append({ key, kind: 'step', cat: BLOCK[c.kind], subject: '', agentId: st.agentId, startedAt: now, phase: { name: 'running', since: now } })
        st.block = key
      }
      en.subject = (en.subject + c.text).slice(-TAIL_CHARS)
      return
    }
    this.closeBlock(st, 'ok', now)
    if (c.kind === 'tool') {
      st.tools.set(c.index, c.id)
      this.fact(c.id, { kind: 'drafted', tool: c.name, agentId: st.agentId }, now)
    }
  }

  stepClosed(ref: string, ended: boolean, now: number) {
    const outcome = ended ? 'ok' : 'interrupted'
    this.close(`${ref}:api`, outcome, now)
    const st = this.steps.get(ref)
    if (st) this.closeBlock(st, outcome, now)
    this.steps.delete(ref)
  }

  fact(id: string, f: Fact, now: number) {
    let en = this.open.get(id)
    if (!en) {
      if (this.byKey.has(id) || !('tool' in f)) return
      const rule = ruleOf(f.tool)
      en = this.append({ key: id, kind: 'tool', cat: rule.cat, tool: f.tool, subject: rule.subject({}, f.tool), startedAt: now, phase: { name: 'drafting', since: now } })
    }
    if ('agentId' in f && f.agentId) en.agentId ??= f.agentId
    switch (f.kind) {
      case 'drafted':
        return
      case 'called':
        en.subject = ruleOf(f.tool).subject(f.args, f.tool) || en.subject
        return this.advance(en, 'pending', now)
      case 'verdict':
        if (f.decision === 'allow') this.advance(en, 'running', now)
        if (f.decision === 'deny') this.finish(en, 'denied', now)
        return
      case 'asked':
        return this.advance(en, 'asking', now)
      case 'answered':
        return f.isAllowed ? this.advance(en, 'running', now) : this.finish(en, 'denied', now)
      case 'ran':
        return this.finish(en, f.outcome, now, f.ms)
      case 'refused':
        return this.finish(en, 'denied', now)
    }
  }

  // The call a PermissionRequest is about, which names no id: that loop's waiting call of the tool, the same arguments first.
  asking(tool: string, input: unknown, agentId?: string): string | undefined {
    const want = ruleOf(tool).subject(argsOf(input), tool)
    const waiting = [...this.open.values()].filter(en => en.kind === 'tool' && en.tool === tool && en.agentId === agentId && en.phase.name === 'pending')
    return (waiting.find(en => en.subject === want) ?? waiting[waiting.length - 1])?.key
  }

  agentOf(id: string) {
    return this.byKey.get(id)?.agentId
  }

  hookEntered(event: string, subject: string, id: string | undefined, agentId: string | undefined, opensTurn: boolean, now: number) {
    const key = `${event}:${id ?? this.nextHook++}`
    this.quiet.set(key, { key, subject: subject ? `${event} ${subject}` : event, agentId, turnAt: opensTurn ? now : this.turnAt, since: now })
    return key
  }

  hookLeft(key: string, outcome: Outcome, now: number) {
    const q = this.quiet.get(key)
    this.quiet.delete(key)
    if (!q || now - q.since < HOOK_FLOOR_MS) return
    this.append({ key, kind: 'hook', cat: 'Hook', subject: q.subject, agentId: q.agentId, turnAt: q.turnAt, startedAt: q.since, phase: { name: 'done', at: now, ms: now - q.since, outcome } })
  }

  loopEnded(agentId: string | undefined, now: number) {
    this.sweep(agentId, now)
    for (const en of [...this.open.values()]) if (en.kind === 'step' && en.agentId === agentId) this.finish(en, 'interrupted', now)
  }

  reset() {
    this.entries = []
    this.byKey.clear()
    this.open.clear()
    this.quiet.clear()
    this.steps.clear()
    this.names.clear()
    this.turnAt = 0
    this.view = { isOpen: false }
  }

  isBusy() {
    return this.open.size > 0 || this.quiet.size > 0
  }

  // The pane's room for history rows, per render: a new size refits the open view about its anchor,
  // and new rows join below while there is room, never moving the rows above them.
  fitTo(rows: number) {
    const fit = Math.max(1, Math.min(MAX_ROWS, Math.floor(rows) || 1))
    const v = this.view
    if (fit !== this.fit) {
      this.fit = fit
      if (v.isOpen && v.anchor === 'bottom') v.top = Math.max(this.firstSeq(), v.bottom - fit + 1)
      else if (v.isOpen) v.bottom = Math.max(v.top, Math.min(this.lastSeq(), v.top + fit - 1))
    }
    if (v.isOpen) v.bottom = Math.max(v.bottom, Math.min(this.lastSeq(), v.top + this.fit - 1))
  }

  isOpen() {
    return this.view.isOpen
  }

  // Opens on the newest entry (its seq, to focus) or closes (null).
  toggle(): number | null {
    if (this.view.isOpen) {
      this.view = { isOpen: false }
      return null
    }
    const bottom = this.lastSeq()
    this.view = { isOpen: true, top: Math.max(this.firstSeq(), bottom - this.fit + 1), bottom, anchor: 'bottom' }
    return this.entries.length ? bottom : null
  }

  // `↓ N 筆新`: `n` rows further down; the new bottom row, to focus.
  newer(n: number): number | null {
    const v = this.view
    if (!v.isOpen || v.bottom >= this.lastSeq()) return null
    v.bottom = Math.min(this.lastSeq(), v.bottom + n)
    v.top = Math.max(this.firstSeq(), v.bottom - this.fit + 1)
    v.anchor = 'bottom'
    return v.bottom
  }

  // `↑ 更早`: `n` rows further up; the new top row, to focus.
  older(n: number): number | null {
    const v = this.view
    if (!v.isOpen || v.top <= this.firstSeq()) return null
    v.top = Math.max(this.firstSeq(), v.top - n)
    v.bottom = Math.min(this.lastSeq(), v.top + this.fit - 1)
    v.anchor = 'top'
    return v.top
  }

  // Rows a press on a sentinel moves: a page that keeps one row of the last in view.
  page() {
    return Math.max(1, this.fit - 1)
  }

  // Read-only: what the band draws now.
  snapshot(now: number): Snapshot {
    const live = [
      ...[...this.open.values()].map(en => this.row(en, now)),
      ...[...this.quiet.values()].filter(q => now - q.since >= HOOK_FLOOR_MS).map(q => this.quietRow(q, now)),
    ].sort((a, b) => a.startedAt - b.startedAt || a.seq - b.seq)
    const newest = this.entries[this.entries.length - 1]
    const v = this.view
    const base = this.firstSeq()
    // A call waiting on the person outranks newer work: it is the one thing they must act on.
    const shown = [...live].reverse().find(r => r.state === 'asking') ?? live[live.length - 1]
    return {
      live: shown ? { row: shown, more: live.length - 1 } : null,
      recent: newest ? this.row(newest, now) : null,
      history: v.isOpen
        ? { rows: this.entries.slice(Math.max(0, v.top - base), Math.max(0, v.bottom - base + 1)).map(en => this.row(en, now)), older: Math.max(0, v.top - base), newer: Math.max(0, this.lastSeq() - v.bottom) }
        : null,
    }
  }

  private firstSeq() {
    return this.entries[0]?.seq ?? this.nextSeq
  }

  private lastSeq() {
    return this.nextSeq - 1
  }

  private label(agentId: string) {
    return `↳${this.names.get(agentId) ?? agentId.slice(0, 6)}`
  }

  private row(en: Entry, now: number): Row {
    const p = en.phase
    return {
      seq: en.seq,
      cat: en.cat,
      who: en.agentId ? this.label(en.agentId) : '',
      subject: en.cat === '思考' || en.cat === '回覆' ? lastLine(en.subject) : oneLine(en.subject),
      state: p.name === 'done' ? 'done' : p.name === 'pending' ? 'running' : p.name,
      ms: p.name === 'done' ? p.ms : now - (en.kind === 'tool' ? p.since : en.startedAt),
      outcome: p.name === 'done' ? p.outcome : undefined,
      startedAt: en.startedAt,
      target: en.kind === 'tool' ? { tool: en.key, agent: en.agentId } : en.agentId ? { agent: en.agentId } : { at: en.turnAt },
    }
  }

  private quietRow(q: Quiet, now: number): Row {
    return { seq: Infinity, cat: 'Hook', who: q.agentId ? this.label(q.agentId) : '', subject: q.subject, state: 'running', ms: now - q.since, startedAt: q.since, target: q.agentId ? { agent: q.agentId } : { at: q.turnAt } }
  }

  private append(f: Pick<Entry, 'key' | 'kind' | 'cat' | 'subject' | 'startedAt' | 'phase'> & Partial<Entry>): Entry {
    const en: Entry = { tool: '', input: '', turnAt: this.turnAt, ...f, seq: this.nextSeq++ }
    this.entries.push(en)
    this.byKey.set(en.key, en)
    if (en.phase.name !== 'done') this.open.set(en.key, en)
    // An open history keeps the rows it shows, up to HARD_CAP.
    const keep = this.view.isOpen ? this.view.top : Infinity
    while (this.entries.length > HARD_CAP || (this.entries.length > CAP && this.entries[0].seq < keep)) this.drop()
    return en
  }

  private drop() {
    const en = this.entries.shift()!
    if (this.byKey.get(en.key) === en) this.byKey.delete(en.key)
    if (this.open.get(en.key) === en) this.open.delete(en.key)
    const v = this.view
    if (v.isOpen && v.top <= en.seq) {
      v.top = en.seq + 1
      v.bottom = Math.max(v.bottom, v.top)
    }
  }

  private advance(en: Entry, to: LivePhase, now: number) {
    const from = en.phase.name
    if (RANK[to] < RANK[from] || (RANK[to] === RANK[from] && !(from === 'asking' && to === 'running'))) return
    en.phase = { name: to, since: now }
  }

  // Duration is the tool's own execution time (PostToolUse's), else what ran since execution began.
  private finish(en: Entry, outcome: Outcome, now: number, ms?: number) {
    const p = en.phase
    if (p.name === 'done') return
    const ran = en.kind !== 'tool' ? now - en.startedAt : p.name === 'running' ? now - p.since : null
    en.phase = { name: 'done', at: now, ms: typeof ms === 'number' ? ms : ran, outcome }
    this.open.delete(en.key)
  }

  private close(key: string, outcome: Outcome, now: number) {
    const en = this.open.get(key)
    if (en) this.finish(en, outcome, now)
  }

  private closeBlock(st: { block: string | null }, outcome: Outcome, now: number) {
    if (st.block) this.close(st.block, outcome, now)
    st.block = null
  }

  private sniffed(id: string | undefined, json: string) {
    const en = id === undefined ? undefined : this.open.get(id)
    if (!en || en.phase.name !== 'drafting' || en.input.length >= SNIFF_BYTES) return
    en.input += json
    const v = sniff(en.input, ruleOf(en.tool).sniff)
    if (v) en.subject = v
  }

  // A loop that moved on: a call still waiting on its verdict or a person was refused; one in flight was cut off.
  private sweep(agentId: string | undefined, now: number) {
    for (const en of [...this.open.values()]) {
      if (en.kind !== 'tool' || en.agentId !== agentId) continue
      const name = en.phase.name
      this.finish(en, name === 'pending' || name === 'asking' ? 'denied' : 'interrupted', now)
    }
  }
}

// event adapters: each turns one engine event into Activity calls, then paints

function paint($: EngineInterface, log: Activity) {
  redraw($)
  if (log.frames || !log.isBusy()) return
  log.frames = $.clock.every(FRAME_MS, () => {
    redraw($)
    if (log.isBusy()) return
    log.frames?.cancel()
    log.frames = null
  })
}

export async function* stepActivity(
  $: EngineInterface,
  log: Activity,
  e: TurnStepInput,
  next: (e: TurnStepInput) => HookStream<TurnStepChunk, TurnStepResult>,
): AsyncGenerator<TurnStepChunk, TurnStepResult> {
  const ref = `${e.agentId ?? ''}:${e.turnId}:${e.index}`
  let at = await $.clock.now()
  log.stepOpened(ref, e.agentId, e.model, at)
  paint($, log)
  const stream = next(e)
  let ended = false
  try {
    for await (const c of stream) {
      if (log.needsTime(ref, c)) {
        at = await $.clock.now()
        log.chunk(ref, c, at)
        paint($, log)
      } else log.chunk(ref, c, at)
      yield c
    }
    ended = true
    return await stream.result
  } finally {
    log.stepClosed(ref, ended, await $.clock.now().catch(() => at))
    paint($, log)
  }
}

export async function classicActivity($: EngineInterface, log: Activity, name: string, e: any, next: (e: any) => Promise<any>): Promise<any> {
  const rule = HOOKS[name]
  if (!rule) return next(e)
  const now = await $.clock.now()
  const id = rule.call?.(e, log)
  if (id && rule.enter) log.fact(id, rule.enter(e), now)
  const key = log.hookEntered(name, rule.subject?.(e) ?? '', id, e.agent_id ?? (id && log.agentOf(id)), rule.opensTurn === true, now)
  paint($, log)
  let r: any
  let outcome: Outcome = 'error'
  try {
    r = await next(e)
    outcome = r?.block || r?.deny ? 'denied' : 'ok'
    return r
  } finally {
    const at = await $.clock.now().catch(() => now)
    const f = id && rule.leave?.(r)
    if (id && f) log.fact(id, f, at)
    log.hookLeft(key, outcome, at)
    paint($, log)
  }
}

export async function checkActivity($: EngineInterface, log: Activity, e: { tool: string; tool_use_id?: string; agentId?: string }, decision: 'allow' | 'ask' | 'deny') {
  if (!e.tool_use_id) return
  log.fact(e.tool_use_id, { kind: 'verdict', tool: e.tool, decision, agentId: e.agentId }, await $.clock.now())
  paint($, log)
}

export async function turnStartActivity($: EngineInterface, log: Activity, agentId: string | undefined) {
  if (agentId === undefined) return log.turnStarted(await $.clock.now())
  if (!log.isNameless(agentId)) return
  const agents = await $.agent.list().catch(() => [])
  log.named(agentId, agents.find(a => a.id === agentId)?.name ?? '')
}

export async function turnEndActivity($: EngineInterface, log: Activity, agentId: string | undefined) {
  log.loopEnded(agentId, await $.clock.now())
  paint($, log)
}

// The history is a pane, not band rows: a pane opened with `focus` takes the keyboard from the prompt,
// which a click on the band never hands over.
export const HISTORY = 'ar-history'
// Body rows asked of the pane inline. The engine gives the pane, the band and the prompt half the
// terminal together; the prompt's four rows, the band's rule and three rows (one spare) and the
// pane's frame come first, so the band never scrolls its own rows away.
const paneRows = (screenRows: number) => Math.max(3, Math.min(14, Math.floor(screenRows / 2) - 12))
const NEWER = 'ar-h-newer'
const OLDER = 'ar-h-older'
const rowKey = (seq: number) => `ar-h-${seq}`
// Arrowing onto a sentinel scrolls the list one row instead, and the ring lands on the row it brought in.
const SENTINELS: Record<string, (log: Activity) => number | null> = { [NEWER]: log => log.newer(1), [OLDER]: log => log.older(1) }

export async function focusActivity($: EngineInterface, log: Activity, e: UiFocusInput, next: (e: UiFocusInput) => Promise<UiFocusResult>): Promise<UiFocusResult> {
  const step = e.plugin === $.plugin.name && e.origin.kind === 'person' ? SENTINELS[e.element ?? ''] : undefined
  if (!step) return next(e)
  void focusOn($, step(log))
  return {}
}

async function focusOn($: EngineInterface, seq: number | null) {
  await Promise.resolve($.ui.invalidate('ui.render')).catch(() => {})
  if (seq !== null) await $.ui.focus({ requestId: HISTORY, key: rowKey(seq) }).catch(() => {})
}

async function openPane($: EngineInterface, log: Activity) {
  await $.ui.open({ id: HISTORY, title: '執行歷史', focus: true, closeOnEscape: true, rows: paneRows(log.screenRows) }).catch(() => {})
}

// Row 3 opens the pane, or gives it the keyboard back when it is already open: a click on the band
// takes the keyboard from the pane first, so the band cannot tell an open pane to close. Its ✕ does.
export async function showHistory($: EngineInterface, log: Activity) {
  const seq = log.isOpen() ? null : log.toggle()
  await openPane($, log)
  await focusOn($, seq)
}

// The pane closed some other way (Esc, its mark, ctrl+x x, an unload): the view closes with it.
export async function closeActivity($: EngineInterface, log: Activity, e: PaneCloseInput, next: (e: PaneCloseInput) => Promise<unknown>) {
  if (e.id === HISTORY && log.isOpen()) {
    log.toggle()
    redraw($)
  }
  return next(e)
}

// Pressing `↓ N 筆新` goes to the newest row; pressing `↑ 更早` a page up.
async function newestHistory($: EngineInterface, log: Activity) {
  await focusOn($, log.newer(Infinity))
}

async function olderHistory($: EngineInterface, log: Activity) {
  await focusOn($, log.older(log.page()))
}

// drawing

export type DrawInput = {
  el: Els
  now: number
  // Cells the band's content may use.
  columns: number
  // The terminal's rows, when the surface says.
  screenRows?: number
  isWorking: boolean
  cwd: string
}

export type HistoryInput = {
  el: Els
  now: number
  // The pane body's cells and rows.
  columns: number
  rows: number
  links: { session: string; pipeline: string }
  cwd: string
  open: (url: string) => void
}

// Paths under the session's folder are shown relative to it; a subagent's rows carry its name.
// A path still being written that has not yet left the folder shows nothing rather than the folder itself.
function subjectOf(r: Row, cwd: string) {
  const inside = cwd && r.subject.startsWith(`${cwd}/`)
  const t = inside ? r.subject.slice(cwd.length + 1) : cwd && r.subject.length > 1 && `${cwd}/`.startsWith(r.subject) ? '' : r.subject
  return r.who ? `${r.who} ${t}` : t
}

// 0.4s · 12.3s · 4m05s · 1h02m
export function fmtDur(ms: number): string {
  if (ms < 59_950) return `${(Math.round(ms / 100) / 10).toFixed(1)}s`
  const t = Math.floor(ms / 1000)
  if (t < 3600) return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, '0')}s`
  return `${Math.floor(t / 3600)}h${String(Math.floor((t % 3600) / 60)).padStart(2, '0')}m`
}

const hms = (ms: number) => {
  const d = new Date(ms)
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map(n => String(n).padStart(2, '0')).join(':')
}

type Shown = { text: string; style: Record<string, unknown> | null }
type Style = Record<string, unknown> | null
// A finished row's time is green when it worked, red when it failed, errored, was refused or cut off.
const TONE: Record<Outcome, Style> = { ok: { color: 'success' }, error: { color: 'error' }, denied: { color: 'error' }, interrupted: { color: 'error' } }
const toneOf = (r: Row): Style => (r.outcome ? TONE[r.outcome] : null)

function durOf(r: Row): Shown {
  if (r.state === 'drafting') return { text: '撰寫中', style: { dimColor: true } }
  if (r.state === 'asking') return { text: `等待允許 ${fmtDur(r.ms ?? 0)}`, style: { color: 'warning' } }
  return { text: r.ms === null ? '' : fmtDur(r.ms), style: null }
}

type Column = { name: string; from: number; width: number | 'rest'; isEnd?: true; cell: (r: Row, subject: string) => string; tone?: (r: Row) => Style }
// History columns, each shown from the band width in `from`.
const COLUMNS: readonly Column[] = [
  { name: 'time', from: 60, width: 8, cell: r => hms(r.startedAt) },
  { name: 'cat', from: 0, width: CAT_W, cell: r => r.cat },
  { name: 'subject', from: 0, width: 'rest', cell: (_, subject) => subject },
  { name: 'dur', from: 0, width: 8, isEnd: true, cell: r => (r.state === 'asking' ? '等待允許' : durOf(r).text), tone: r => (r.state === 'done' ? toneOf(r) : durOf(r).style) },
]

function urlOf(t: Target, links: DrawInput['links']): string {
  if (!links.pipeline) return ''
  if ('tool' in t) return `${links.pipeline}&tool=${t.tool}${t.agent ? `&agent=${t.agent}` : ''}`
  if ('agent' in t) return `${links.pipeline}&agent=${t.agent}`
  return t.at ? `${links.pipeline}&at=${t.at / 1000}` : links.session
}

const styledIn = (Text: Els['Text']) => (x: Shown) => (x.style ? <Text {...x.style}>{x.text}</Text> : x.text)

// Row 3: the newest thing in flight and how many others; once nothing is, the latest done, dim,
// and nothing before the session has run anything. Pressing it opens the history pane.
// It spans the band's width, its time at the right edge.
export function drawActivity($: EngineInterface, log: Activity, d: DrawInput): RenderNode[] {
  const { Box, Button, Text } = d.el
  if (d.screenRows) log.screenRows = d.screenRows
  const s = log.snapshot(d.now)
  const working = d.isWorking || s.live !== null
  const show = () => void showHistory($, log)
  const subject = (r: Row) => subjectOf(r, d.cwd)
  const styled = styledIn(Text)
  const below: RenderNode[] = []
  const row = s.live?.row ?? s.recent
  // ctrl+x tab lands on row 3, so Enter opens the history from the keyboard.
  const ring = true as const
  if (working || row) {
    const head = `${pad(row?.cat ?? '', CAT_W)} `
    if (!row) below.push(<Box key="ar-live-row" height={1}><Button key="ar-live" hover={hl('ar-live')} plain autoFocus={ring} onPress={show}><Text dimColor>…</Text></Button></Box>)
    else if (s.live) {
      const right = durOf(row)
      const more = s.live.more > 0 ? ` +${s.live.more}` : ''
      const room = d.columns - width(head) - 2 - width(right.text) - width(more)
      below.push(
        <Box key="ar-live-row" height={1} overflow="hidden">
          <Button key="ar-live" hover={hl('ar-live')} plain autoFocus={ring} onPress={show}>{`${head}${pad(subject(row), room)}  `}{styled(right)}{more}</Button>
        </Box>,
      )
    } else {
      const right = durOf(row).text
      const room = d.columns - width(head) - 2 - width(right)
      below.push(
        <Box key="ar-live-row" height={1} overflow="hidden">
          <Button key="ar-live" hover={hl('ar-live')} plain autoFocus={ring} onPress={show}><Text dimColor>{`${head}${pad(subject(row), room)}  `}</Text>{styled({ text: right, style: toneOf(row) })}</Button>
        </Box>,
      )
    }
  }

  return below
}

// The history pane's body: one row per entry, its page's sentinels, the newest row holding the ring first.
export function drawHistory($: EngineInterface, log: Activity, d: HistoryInput): RenderNode {
  const { Box, Button, Text } = d.el
  // Both page sentinels always hold their rows, so a sentinel appearing never refits the list.
  log.fitTo(d.rows - 2)
  const hist = log.snapshot(d.now).history
  const styled = styledIn(Text)
  if (!hist) return <Text dimColor>尚無紀錄</Text>
  const cols = COLUMNS.filter(c => d.columns >= c.from)
  const fixed = cols.reduce((n, c) => n + (c.width === 'rest' ? 0 : c.width), 0) + cols.length + 1
  const rest = Math.max(8, Math.min(SUBJECT_MAX, d.columns - fixed))
  const line = (r: Row) => cols.map((c, i) => {
    const text = (i ? ' ' : '') + pad(c.cell(r, subjectOf(r, d.cwd)), c.width === 'rest' ? rest : c.width, c.isEnd)
    return styled({ text, style: c.tone?.(r) ?? null })
  })
  const bottom = hist.rows[hist.rows.length - 1]?.seq
  return (
    <Box key="ar-history" flexDirection="column">
      {hist.older > 0 ? <Button key={OLDER} hover={hl(OLDER)} plain onPress={() => void olderHistory($, log)}><Text dimColor>{`↑ 更早 ${hist.older} 筆`}</Text></Button> : null}
      {hist.rows.length === 0 ? <Text key="ar-h-empty" dimColor>尚無紀錄</Text> : null}
      {hist.rows.map(r => {
        const url = urlOf(r.target, d.links)
        return (
          <Button key={rowKey(r.seq)} hover={hl(rowKey(r.seq))} plain autoFocus={r.seq === bottom ? true : undefined} onPress={() => url && d.open(url)}>
            {line(r)}
          </Button>
        )
      })}
      {hist.newer > 0 ? <Button key={NEWER} hover={hl(NEWER)} plain onPress={() => void newestHistory($, log)}><Text dimColor>{`↓ ${hist.newer} 筆新`}</Text></Button> : null}
    </Box>
  )
}

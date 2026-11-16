import { expect, mock, test } from 'claude-code/testing'

// Row 3 and the execution history, driven through the engine's own events: model steps,
// tool calls with their classic hooks and verdicts, subagent loops, turn ends and session ends.
const SLOW = { timeoutMs: 20_000 }
const TURN = { turnId: 't', answer: 'done', durationMs: 1, isAborted: false, usage: null }
const T0 = 1_762_912_345_120
const SID = '3f8a1c2e-5b7d-4e90-9a6c-1d2e3f4a5b6c'
const PIPELINE = 'http://localhost:8787/pipeline?session=3f8a1c2e'
const STATS = [
  'Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 20% · 5h 已用 5%(49m) · 7d 已用 27%(3d20h)',
  '3f8a1c2e #4 · 本回合 context +2k（平常 +6k）',
].join('\n')

type Run = { ms?: number; fail?: boolean; hold?: Promise<void> }
type World = {
  runs: string[][]
  calls: { tool: string; id: string; agentId?: string }[]
  // The history pane's opens and closes, as asked of the engine.
  panes: [string, any][]
  // How the tool beneath runs, by the call's command or path; a plain run otherwise.
  plan: Record<string, Run>
  verdict: (e: any) => 'allow' | 'ask' | 'deny'
  // The settings hooks beneath, by classic event name.
  classic: Record<string, (e: any) => any>
}

const gate = () => {
  let open = () => {}
  const held = new Promise<void>(r => (open = r))
  return { held, open }
}

function setup($: any, on: any) {
  const clock = mock.clock(on, { now: T0 })
  const w: World = { runs: [], calls: [], panes: [], plan: {}, verdict: () => 'allow', classic: {} }
  on('turn.complete', () => ({ text: '' }))
  on('turn.start', (_: any, e: any) => ({ turnId: e.turnId }))
  on('ui.render', () => ({ type: 'Text', props: {}, children: ['base'] }))
  on('ui.focus', () => ({}))
  on('ui.open', (_: any, e: any) => (w.panes.push(['open', e]), { value: { isPlaced: true } }))
  on('ui.close', (_: any, e: any) => (w.panes.push(['close', e]), { value: undefined }))
  on('session.end', () => ({ sessionId: SID }))
  on('session.usage', () => ({ value: { context: { percent: 20, window: 1_000_000 }, rateLimits: [], cost: {} } }))
  on('session.id', () => ({ value: SID }))
  on('session.model', () => ({ value: 'Opus 5.5 (1M context)' }))
  on('session.cwd', () => ({ value: '/tmp/proj' }))
  on('fs.write', () => ({ value: undefined }))
  on('settings.read', () => ({ value: { effortLevel: 'medium' } }))
  on('agent.list', () => ({ value: [{ id: 'a1b2c3d4e5f6', description: 'scan', type: 'Explore', status: 'running', name: 'scout' }] }))
  on('process.run', (_: any, e: any) => {
    const argv: string[] = e.argv ?? e.command
    w.runs.push(argv)
    return { value: { exitCode: 0, stdout: argv[0] === 'open' ? '' : STATS, stderr: '' } }
  })
  on('tool.check', (_: any, e: any) => ({ decision: w.verdict(e) }))
  on('classic.*', (_: any, e: any, next: any) => w.classic[next.event.slice('classic.'.length)]?.(e) ?? {})
  on('tool.call', async (_: any, e: any) => {
    w.calls.push({ tool: e.tool, id: e.tool_use_id, agentId: e.agentId })
    const r = w.plan[e.command ?? e.file_path ?? ''] ?? {}
    if (r.hold) await r.hold
    const base = { tool_name: e.tool, tool_input: {}, tool_use_id: e.tool_use_id, duration_ms: r.ms ?? 100, ...(e.agentId ? { agent_id: e.agentId } : {}) }
    if (r.fail) await $.classic.PostToolUseFailure({ ...base, error: 'boom' })
    else await $.classic.PostToolUse({ ...base, tool_response: {} })
    return { result: {} }
  })
  return { clock, w }
}

const pluginOf = (n: any): string | undefined => n?.press?.plugin ?? n?.children?.map?.(pluginOf).find(Boolean)

// The band and the history pane, searched and pressed as one screen.
async function mount($: any, opts: { isWorking?: boolean; columns?: number; maxRows?: number; paneRows?: number } = {}) {
  const columns = opts.columns ?? 100
  const viewport = { columns: columns + 5, rows: 40 }
  const spec = { component: 'AbovePrompt', surface: 'terminal', requestId: 'band', viewport }
  const props = { hasSurvey: false, isWorking: opts.isWorking ?? false, maxRows: opts.maxRows ?? 40, bodyColumns: columns }
  const probe = await $.ui.mount({ ...spec, props, plugin: '' })
  const plugin = pluginOf(await probe.drawn())
  await probe.unmount()
  const band = await $.ui.mount({ ...spec, props, plugin })
  const pane = await $.ui.mount({
    component: 'Pane', surface: 'terminal', requestId: 'ar-history', viewport, plugin,
    props: { title: '執行歷史', isFocused: true, bodyColumns: columns, placement: 'inline', scroll: { offset: 0, bodyRows: opts.paneRows ?? 40 }, view: {} },
  })
  const ui = {
    band,
    pane,
    find: async (q: any) => (await band.find(q)) ?? (await pane.find(q)),
    findAll: async (q: any) => [...(await band.findAll(q)), ...(await pane.findAll(q))],
    press: async (q: any) => ((await band.find(q)) ? band.press(q) : pane.press(q)),
    unmount: async () => (await band.unmount(), await pane.unmount()),
  }
  return { ui, plugin: plugin! }
}

async function ready($: any, clock: any) {
  await $.turn.complete(TURN)
  await clock.settle()
}

const text = async (ui: any, key: string) => (await ui.find({ key }))?.text as string | undefined
const rows = async (ui: any) => (await ui.findAll({ type: 'Button' })).filter((b: any) => /^ar-h-\d+$/.test(b.key ?? ''))
const rowTexts = async (ui: any) => (await rows(ui)).map((b: any) => b.text as string)
const commands = async (ui: any) => (await rowTexts(ui)).map(t => /Bash\s+(\S+)/.exec(t)?.[1])
const cellsOf = (t: string) => [...t].reduce((n, ch) => n + (/[\u2e80-\ua4cf\uff00-\uff60]/.test(ch) ? 2 : 1), 0)

// A model step whose chunks the test hands over one at a time.
function stepper(on: any) {
  const queue: any[] = []
  let wake = () => {}
  let isDone = false
  on('turn.step', async function* (_: any, e: any) {
    for (;;) {
      if (queue.length) yield queue.shift()
      else if (isDone) break
      else await new Promise<void>(r => (wake = r))
    }
    return { turnId: e.turnId, index: e.index, answer: '', toolUses: [], stopReason: 'end_turn', usage: null }
  })
  return {
    push: (...cs: any[]) => (queue.push(...cs), wake()),
    end: () => ((isDone = true), wake()),
    run: ($: any, e: any = {}) => (async () => {
      for await (const _ of $.turn.step({ turnId: 'turn-1', index: 0, model: 'claude-opus-5-5', messageCount: 1, ...e })) void _
    })(),
  }
}

test('a running Bash call shows on row 3 with its live time, then lands in the history with its own duration', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  const g = gate()
  w.plan['git status -sb && git log --oneline -5'] = { hold: g.held, ms: 3150 }
  const { ui } = await mount($, { isWorking: true })
  const call = $.tool.call({ tool: 'Bash', command: 'git status -sb && git log --oneline -5' } as any)
  await clock.advance(3200)
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+git status -sb && git log --oneline -5\s+3\.2s$/)
  await clock.advance(5000)
  expect(await text(ui, 'ar-live')).toMatch(/8\.2s$/)
  g.open()
  await call
  await clock.settle()
  await ui.press({ key: 'ar-live' })
  const [row] = await rows(ui)
  expect(row.text).toMatch(/^\d\d:\d\d:\d\d Bash\s+git status -sb && git log --oneline -5\s+3\.2s$/)
  expect((await ui.findAll({ type: 'Text', text: /3\.2s$/ })).find((x: any) => /3\.2s$/.test(x.text))?.props.color).toBe('success')
  await ui.press({ key: row.key })
  expect(w.runs).toContainEqual(['open', `${PIPELINE}&tool=${w.calls[0].id}`])
  await ui.unmount()
})

test('each tool lands in its category; any other tool is Agent, led by its name', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  const calls: any[] = [
    { tool: 'Read', file_path: '/tmp/proj/src/auth.ts' },
    { tool: 'Grep', pattern: 'verifyToken', path: 'src' },
    { tool: 'Edit', file_path: '/tmp/proj/src/auth.ts', old_string: 'a', new_string: 'b' },
    { tool: 'Bash', command: 'npm   test\n  -- --watch' },
    { tool: 'Agent', description: 'scan auth', prompt: 'look', subagent_type: 'Explore' },
    { tool: 'Skill', skill: 'pdf' },
  ]
  for (const c of calls) await $.tool.call(c)
  await clock.settle()
  const { ui } = await mount($)
  await ui.press({ key: 'ar-live' })
  const got = (await rowTexts(ui)).map(t => t.replace(/^\d\d:\d\d:\d\d /, '').replace(/\s+0\.1s$/, '').replace(/\s+/g, ' '))
  expect(got).toEqual(['Read src/auth.ts', 'Read verifyToken src', 'Write src/auth.ts', 'Bash npm test -- --watch', 'Agent Explore scan auth', 'Agent Skill pdf'])
  expect(w.calls.length).toBe(calls.length)
  await ui.unmount()
})

test('a model step shows API, then 思考 and 回覆 with their latest line, then a call being written', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  const step = stepper(on)
  await ready($, clock)
  const { ui } = await mount($, { isWorking: true })
  await $.turn.start({ text: 'hi', turnId: 'turn-1' })
  const run = step.run($)
  await clock.advance(1200)
  expect(await text(ui, 'ar-live')).toMatch(/^API\s+claude-opus-5-5\s+1\.2s$/)
  step.push({ kind: 'thinking', index: 0, text: 'Let me look\nat the failing test' })
  await clock.advance(700)
  expect(await text(ui, 'ar-live')).toMatch(/^思考\s+at the failing test\s+0\.7s$/)
  step.push({ kind: 'text', index: 1, text: 'Found it.' }, { kind: 'text', index: 1, text: ' Fixing' })
  await clock.settle()
  expect(await text(ui, 'ar-live')).toMatch(/^回覆\s+Found it\. Fixing\s+0\.0s$/)
  step.push({ kind: 'tool', index: 2, id: 'toolu_W', name: 'Write' })
  await clock.settle()
  expect(await text(ui, 'ar-live')).toMatch(/^Write\s+撰寫中$/)
  step.push({ kind: 'input', index: 2, json: '{"file_path":"/tmp/proj/src/x.ts","con' })
  await clock.advance(100)
  expect(await text(ui, 'ar-live')).toMatch(/^Write\s+src\/x\.ts\s+撰寫中$/)
  step.push({ kind: 'stop', stopReason: 'tool_use', usage: null })
  step.end()
  await run
  await $.turn.complete({ ...TURN, turnId: 'turn-1', isAborted: true })
  await clock.settle()
  await ui.press({ key: 'ar-live' })
  const got = (await rowTexts(ui)).map(t => t.replace(/^\d\d:\d\d:\d\d /, '').replace(/\s+/g, ' ').trim())
  expect(got).toEqual(['API claude-opus-5-5 1.2s', '思考 at the failing test 0.7s', '回覆 Found it. Fixing 0.0s', 'Write src/x.ts'])
  await ui.unmount()
})

test('subagent calls carry ↳ with the agent name, or a short id when it has none', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  await $.turn.start({ text: 'go', turnId: 'sub-1', agentId: 'a1b2c3d4e5f6' })
  const g = gate()
  w.plan.ls = { hold: g.held }
  const { ui } = await mount($)
  const call = $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'a1b2c3d4e5f6' } as any)
  await clock.settle()
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+↳scout ls\s+0\.0s$/)
  g.open()
  await call
  await $.tool.call({ tool: 'Read', file_path: '/etc/hosts', agentId: 'ffeeddccbbaa99' } as any)
  await clock.settle()
  expect(await text(ui, 'ar-live')).toMatch(/^Read\s+↳ffeedd \/etc\/hosts\s+\d+\.\ds$/)
  await ui.unmount()
})

test('a call put to a person shows 等待允許; its duration is execution only', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  const g = gate()
  w.verdict = e => (e.input?.command === 'rm -rf build' ? 'ask' : 'allow')
  w.classic.PermissionRequest = async () => (await g.held, { decision: { behavior: 'allow' } })
  w.plan['rm -rf build'] = { ms: 500 }
  const { ui } = await mount($, { isWorking: true })
  const call = $.tool.call({ tool: 'Bash', command: 'rm -rf build' } as any)
  await clock.advance(2000)
  // The kit's PermissionRequest hook stands for the person too, so its run is the +1.
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+rm -rf build\s+等待允許 2\.0s \+1$/)
  expect((await ui.findAll({ type: 'Text', text: /等待允許/ }))[0].props.color).toBe('warning')
  g.open()
  await call
  await clock.settle()
  await ui.press({ key: 'ar-live' })
  expect((await rowTexts(ui))[0]).toMatch(/Bash\s+rm -rf build\s+0\.5s$/)
  await ui.unmount()
})

test('parallel calls show the newest with +N; refused and failed calls show their time in red', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  const [a, b] = [gate(), gate()]
  w.plan['sleep 1'] = { hold: a.held }
  w.plan['sleep 2'] = { hold: b.held }
  w.plan['false'] = { fail: true }
  w.verdict = e => (e.input?.command === 'rm -rf /' ? 'deny' : 'allow')
  const { ui } = await mount($, { isWorking: true })
  const one = $.tool.call({ tool: 'Bash', command: 'sleep 1' } as any)
  await clock.advance(300)
  const two = $.tool.call({ tool: 'Bash', command: 'sleep 2' } as any)
  await clock.advance(200)
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+sleep 2\s+0\.2s \+1$/)
  a.open()
  b.open()
  await Promise.all([one, two])
  await $.tool.call({ tool: 'Bash', command: 'rm -rf /' } as any)
  await $.tool.call({ tool: 'Bash', command: 'false' } as any)
  await clock.settle()
  await ui.press({ key: 'ar-live' })
  const tones = (await ui.pane.findAll({ type: 'Text' })).map((t: any) => t.props.color).filter((c: any) => c === 'success' || c === 'error')
  expect(tones.slice(-2)).toEqual(['error', 'error'])
  await ui.unmount()
})

test('row 3 spans the band with its time at the right edge, short or long', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  await ready($, clock)
  await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  await clock.settle()
  for (const columns of [60, 120]) {
    const { ui } = await mount($, { columns })
    const row = (await text(ui, 'ar-live'))!
    expect(cellsOf(row)).toBe(columns - 2)
    expect(row).toMatch(/^Bash\s+ls\s{20,}\d+\.\ds$/)
    await ui.unmount()
  }
})

test('idle, row 3 keeps the last row, dim, and row 2 gives it no room', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  await ready($, clock)
  await $.tool.call({ tool: 'Bash', command: 'git status -sb' } as any)
  await clock.settle()
  const wide = await mount($, { columns: 120 })
  expect(await text(wide.ui, 'ar-live')).toMatch(/^Bash\s+git status -sb\s+\d+\.\ds$/)
  expect(await wide.ui.find({ key: 'ar-cell-last' })).toBeUndefined()
  await wide.ui.unmount()
  const narrow = await mount($, { columns: 52 })
  expect(await text(narrow.ui, 'ar-cell-cache')).toBe('◷ 59m59s/1h')
  expect(await text(narrow.ui, 'ar-live')).toMatch(/^Bash\s+git status -sb/)
  await narrow.ui.unmount()
})

test('a reply that ends in a code fence shows its last line of words', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  const step = stepper(on)
  await ready($, clock)
  const { ui } = await mount($, { isWorking: true })
  await $.turn.start({ text: 'hi', turnId: 'turn-1' })
  const run = step.run($)
  step.push({ kind: 'text', index: 0, text: 'Run this:\n```bash\nnpm test\n```\n' })
  await clock.settle()
  expect(await text(ui, 'ar-live')).toMatch(/^回覆\s+npm test\s/)
  step.end()
  await run
  await ui.unmount()
})

test('idle after a turn that ran no command, row 3 keeps its last row, dim', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  const step = stepper(on)
  await ready($, clock)
  await $.turn.start({ text: 'hi', turnId: 'turn-1' })
  const run = step.run($)
  step.push({ kind: 'text', index: 0, text: '嗨！今天要做什麼？' })
  step.end()
  await run
  await $.turn.complete({ turnId: 'turn-1' } as any)
  await clock.settle()
  const { ui } = await mount($, { columns: 120 })
  expect(await text(ui, 'ar-live')).toMatch(/^回覆\s+嗨！今天要做什麼？\s/)
  await ui.unmount()
  const fresh = await mount($, { columns: 120 })
  await $.session.end({ reason: 'clear' } as any)
  await clock.advance(500)
  expect(await fresh.ui.find({ key: 'ar-live' })).toBeUndefined()
  await fresh.ui.unmount()
})

test('row 3 opens the history pane with the keyboard on the newest row, and gives it the keyboard again', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  for (const command of ['ls', 'pwd', 'whoami']) await $.tool.call({ tool: 'Bash', command } as any)
  await clock.settle()
  const { ui } = await mount($)
  expect(await rows(ui)).toEqual([])
  await ui.press({ key: 'ar-live' })
  const open = await rows(ui)
  expect(open.map((b: any) => b.text.replace(/^\S+ /, '').split(/\s+/)[1])).toEqual(['ls', 'pwd', 'whoami'])
  expect(open.map((b: any) => b.props.autoFocus)).toEqual([undefined, undefined, true])
  expect(w.panes).toEqual([['open', expect.objectContaining({ id: 'ar-history', focus: true, closeOnEscape: true })]])
  expect(await ui.band.find({ key: 'ar-h-3' })).toBeUndefined()
  // Row 3 still shows the last row; pressing it again hands the open pane the keyboard, list unmoved.
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+whoami\s/)
  await ui.press({ key: 'ar-live' })
  expect((await rows(ui)).length).toBe(3)
  expect(w.panes.map(([k, e]) => [k, e.focus])).toEqual([['open', true], ['open', true]])
  await ui.unmount()
})

test('the open list grows into free room; once full, new rows wait below as ↓ N 筆新, one row per arrow', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  await ready($, clock)
  for (const command of ['ls', 'pwd']) await $.tool.call({ tool: 'Bash', command } as any)
  await clock.settle()
  // A 5-row pane: three rows for the list, two for the page rows.
  const { ui, plugin } = await mount($, { paneRows: 5 })
  await ui.press({ key: 'ar-live' })
  expect(await commands(ui)).toEqual(['ls', 'pwd'])
  await $.tool.call({ tool: 'Bash', command: 'whoami' } as any)
  await clock.settle()
  expect(await commands(ui)).toEqual(['ls', 'pwd', 'whoami'])
  expect(await ui.find({ key: 'ar-h-newer' })).toBeUndefined()
  for (const command of ['date', 'uptime']) await $.tool.call({ tool: 'Bash', command } as any)
  await clock.settle()
  expect(await commands(ui)).toEqual(['ls', 'pwd', 'whoami'])
  expect(await text(ui, 'ar-h-newer')).toBe('↓ 2 筆新')
  const arrow = (element: string) => $.ui.focus({ component: 'Pane', requestId: 'ar-history', plugin, element, origin: { kind: 'person' } } as any)
  expect(await arrow('ar-h-newer')).toEqual({})
  await clock.settle()
  expect(await commands(ui)).toEqual(['pwd', 'whoami', 'date'])
  expect(await text(ui, 'ar-h-newer')).toBe('↓ 1 筆新')
  expect(await text(ui, 'ar-h-older')).toBe('↑ 更早 1 筆')
  await arrow('ar-h-older')
  await clock.settle()
  expect(await commands(ui)).toEqual(['ls', 'pwd', 'whoami'])
  expect(await text(ui, 'ar-h-newer')).toBe('↓ 2 筆新')
  // Pressing it goes to the newest row.
  await ui.press({ key: 'ar-h-newer' })
  expect(await commands(ui)).toEqual(['whoami', 'date', 'uptime'])
  expect(await ui.find({ key: 'ar-h-newer' })).toBeUndefined()
  await ui.unmount()
})
test('Enter on a row opens its cost: the call itself, in a subagent with its agent, else its turn or agent page', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  const step = stepper(on)
  await ready($, clock)
  await clock.advance(10_000)
  await $.turn.start({ text: 'hi', turnId: 'turn-1' })
  const turnAt = T0 + 10_000
  const run = step.run($)
  step.push({ kind: 'text', index: 0, text: 'ok' }, { kind: 'stop', stopReason: 'end_turn', usage: null })
  step.end()
  await run
  await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  await $.tool.call({ tool: 'Bash', command: 'pwd', agentId: 'a1b2c3d4e5f6' } as any)
  for await (const _ of $.turn.step({ turnId: 'sub-1', index: 0, model: 'claude-haiku-5-5', messageCount: 1, agentId: 'a1b2c3d4e5f6' })) void _
  await clock.settle()
  const { ui } = await mount($)
  await ui.press({ key: 'ar-live' })
  for (const r of await rows(ui)) await ui.press({ key: r.key })
  const opened = w.runs.filter(a => a[0] === 'open').map(a => a[1])
  expect(opened).toEqual([
    `${PIPELINE}&at=${turnAt / 1000}`,
    `${PIPELINE}&at=${turnAt / 1000}`,
    `${PIPELINE}&tool=${w.calls[0].id}`,
    `${PIPELINE}&tool=${w.calls[1].id}&agent=a1b2c3d4e5f6`,
    `${PIPELINE}&agent=a1b2c3d4e5f6`,
  ])
  await ui.unmount()
})

test('command hooks show as Hook rows only once they run past the noise floor', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  w.classic.Stop = async () => (await clock.sleep(1500), {})
  const { ui } = await mount($, { isWorking: true })
  await $.classic.SubagentStop({ stop_hook_active: false, agent_id: 'x', agent_type: 'y', agent_transcript_path: '' } as any)
  const stop = $.classic.Stop({ stop_hook_active: false } as any)
  await clock.advance(400)
  expect(await text(ui, 'ar-live')).toMatch(/^Hook\s+Stop\s+0\.4s$/)
  await clock.advance(1100)
  await stop
  await clock.settle()
  await ui.press({ key: 'ar-live' })
  expect((await rowTexts(ui)).map(t => t.replace(/^\S+ /, '').replace(/\s+/g, ' '))).toEqual(['Hook Stop 1.5s'])
  await ui.unmount()
})

test('the history keeps the last 500 entries', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  const step = stepper(on)
  await ready($, clock)
  const run = step.run($)
  for (let i = 0; i < 510; i++) step.push({ kind: 'text', index: i, text: `block ${i}` })
  step.end()
  await run
  await clock.settle()
  const { ui } = await mount($, { isWorking: true })
  await ui.press({ key: 'ar-live' })
  const shown = (await rows(ui)).length
  expect(shown).toBeLessThan(40)
  expect(await text(ui, 'ar-h-older')).toBe(`↑ 更早 ${500 - shown} 筆`)
  while (await ui.find({ key: 'ar-h-older' })) await ui.press({ key: 'ar-h-older' })
  expect((await rowTexts(ui))[0]).toMatch(/回覆\s+block 10\s/)
  await ui.unmount()
})

test('a new session starts an empty history', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  await ready($, clock)
  await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  await clock.settle()
  const { ui } = await mount($)
  expect(await text(ui, 'ar-live')).toMatch(/^Bash\s+ls\s/)
  await $.session.end({ reason: 'clear' } as any)
  await clock.advance(500)
  expect(await ui.find({ key: 'ar-live' })).toBeUndefined()
  await ui.unmount()
})

test('row 3 and the history rows fit their width', SLOW, async ($, on) => {
  const { clock, w } = setup($, on)
  await ready($, clock)
  const long = `grep -rn "a very long pattern that will not fit" ${'src/some/deep/folder/'.repeat(8)}`
  const g = gate()
  w.plan[long] = { hold: g.held }
  const call = $.tool.call({ tool: 'Bash', command: long } as any)
  await clock.settle()
  for (const columns of [40, 70, 120]) {
    const { ui } = await mount($, { columns, isWorking: true })
    const live = (await text(ui, 'ar-live'))!
    // The band indents its rows two cells.
    expect(cellsOf(live)).toBe(columns - 2)
    expect(live).toMatch(/…\s+0\.0s$/)
    await ui.press({ key: 'ar-live' })
    for (const t of await rowTexts(ui)) expect(cellsOf(t)).toBeLessThanOrEqual(columns)
    expect(/^\d\d:/.test((await rowTexts(ui))[0])).toBe(columns >= 70)
    await ui.press({ key: 'ar-live' })
    await ui.unmount()
  }
  g.open()
  await call
})

test('the history opens on the newest rows that fit the pane, and new ones wait below without moving it', SLOW, async ($, on) => {
  const { clock } = setup($, on)
  await ready($, clock)
  const c = (i: number) => `c${i}`
  for (let i = 0; i < 20; i++) await $.tool.call({ tool: 'Bash', command: c(i) } as any)
  await clock.settle()
  // An 8-row pane: the two page rows leave six for the list.
  const { ui } = await mount($, { paneRows: 8 })
  await ui.press({ key: 'ar-live' })
  expect(await commands(ui)).toEqual([14, 15, 16, 17, 18, 19].map(c))
  expect(await text(ui, 'ar-h-older')).toBe('↑ 更早 14 筆')
  expect(await ui.find({ key: 'ar-h-newer' })).toBeUndefined()
  expect((await rows(ui)).at(-1).props.autoFocus).toBe(true)
  for (let i = 20; i < 23; i++) await $.tool.call({ tool: 'Bash', command: c(i) } as any)
  await clock.settle()
  expect(await commands(ui)).toEqual([14, 15, 16, 17, 18, 19].map(c))
  expect(await text(ui, 'ar-h-newer')).toBe('↓ 3 筆新')
  // A page up keeps the old top in view, at the bottom.
  await ui.press({ key: 'ar-h-older' })
  expect(await commands(ui)).toEqual([9, 10, 11, 12, 13, 14].map(c))
  expect(await text(ui, 'ar-h-older')).toBe('↑ 更早 9 筆')
  expect(await text(ui, 'ar-h-newer')).toBe('↓ 8 筆新')
  await ui.press({ key: 'ar-h-newer' })
  expect(await commands(ui)).toEqual([17, 18, 19, 20, 21, 22].map(c))
  expect(await ui.find({ key: 'ar-h-newer' })).toBeUndefined()
  await ui.unmount()
  // A taller pane shows more above the same newest row.
  const tall = await mount($, { paneRows: 10 })
  expect(await commands(tall.ui)).toEqual([15, 16, 17, 18, 19, 20, 21, 22].map(c))
  await tall.ui.unmount()
})

import { expect, mock, test } from 'claude-code/testing'

// Branch coverage for hooks/auto_recap.tsx: quota/cache countdown formats, error paths, autoCompact edges,
// the picker, the context hotkey row and the pager. The suite does not touch the real scorecard script.
const SLOW = { timeoutMs: 20_000 }
const TURN = { turnId: 't', answer: 'done', durationMs: 1, isAborted: false, usage: null }
const HOUR = 60 * 60_000
const SID = '3f8a1c2e-5b7d-4e90-9a6c-1d2e3f4a5b6c'
const ROW1 = 'Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 35% · 5h 已用 5%(49m) · 7d 已用 27%(3d20h)'
const SUBAGENT = '本回合已委派 agent-perform:code-finder（haiku）'
const SESSION = '3f8a1c2e #115 · 花費 $28.51 · 浪費 $0.42 · 本回合 context +86k（平常 +6k）'
const stats = (row1 = ROW1, ...rest: string[]) => [row1, ...(rest.length ? rest : [SUBAGENT, SESSION])].join('\n')

type Cfg = {
  percent?: number | undefined
  rateLimits?: any | ((now: number) => any)
  out?: string
  exitCode?: number
  runFails?: boolean
  version?: string | null
  settings?: () => any
  fsWrite?: () => any
  config?: any[]
  compactFails?: boolean
  commandText?: string
}

function setup(on: any, cfg: Cfg = {}) {
  const clock = mock.clock(on)
  const log: string[] = []
  const runs: string[][] = []
  const stdins: string[] = []
  const state = { percent: 'percent' in cfg ? cfg.percent : 35 }
  on('turn.complete', () => ({ text: '' }))
  on('prompt.submit', ($: any, e: any) => ({ text: e.text }))
  on('ui.render', () => ({ type: 'Text', props: {}, children: ['base'] }))
  on('session.usage', () => ({
    value: {
      context: { percent: state.percent, window: 1_000_000 },
      rateLimits: (typeof cfg.rateLimits === 'function' ? cfg.rateLimits(clock.now()) : cfg.rateLimits) ?? [{ kind: 'five_hour', percentUsed: 5, resetsAt: new Date(clock.now() + 49 * 60_000).toISOString() }],
      cost: {},
    },
  }))
  on('session.measure', ($: any, e: any) => ({ changed: e.changed }))
  on('session.id', () => ({ value: SID }))
  on('session.model', () => ({ value: 'Opus 5.5 (1M context)' }))
  on('session.cwd', () => ({ value: '/tmp/proj' }))
  on('fs.write', () => (cfg.fsWrite ? cfg.fsWrite() : { value: undefined }))
  on('settings.read', () => (cfg.settings ? cfg.settings() : { value: { effortLevel: 'medium' } }))
  on('process.run', ($: any, e: any) => {
    const argv: string[] = e.argv ?? e.command
    runs.push(argv)
    if (argv[0] === 'cat') {
      return cfg.version === null
        ? { value: { exitCode: 1, stdout: '', stderr: 'no such file' } }
        : { value: { exitCode: 0, stdout: JSON.stringify({ version: cfg.version ?? '9.9.9' }), stderr: '' } }
    }
    if (argv[0] === 'open') return { value: { exitCode: 0, stdout: '', stderr: '' } }
    stdins.push(e.init?.stdin)
    if (cfg.runFails) throw new Error('spawn bin/ap ENOENT')
    return { value: { exitCode: cfg.exitCode ?? 0, stdout: cfg.out ?? stats(), stderr: '' } }
  })
  on('config.list', () => ({ value: cfg.config ?? [{ key: 'model', label: 'Model', kind: 'choice', value: 'opus[1m]', options: ['opus[1m]', 'sonnet'] }] }))
  on('config.set', ($: any, e: any) => {
    log.push(`config:${e.key}=${e.value}`)
    return { value: e.value }
  })
  on('command.run', ($: any, e: any) => {
    log.push(e.args ? `${e.command} ${e.args}` : e.command)
    if (cfg.compactFails && e.command === 'clear') throw new Error('clear failed')
    return { text: e.command === 'recap' ? cfg.commandText ?? 'goal and next step' : '' }
  })
  on('session.compact', ($: any, e: any) => {
    log.push(`compact:${e.instructions ?? ''}`)
    return { skip: 'test' }
  })
  return { clock, log, runs, stdins, state }
}

const PROPS = { hasSurvey: false, isWorking: false, maxRows: 10 }
const pluginOf = (n: any): string | undefined => n?.press?.plugin ?? n?.children?.map?.(pluginOf).find(Boolean)

async function mod($: any, columns = 400, props: any = PROPS) {
  const spec = { component: 'AbovePrompt', surface: 'terminal', viewport: { columns, rows: 30 } }
  const probe = await $.ui.mount({ ...spec, props: PROPS, plugin: '' })
  const plugin = pluginOf(await probe.drawn())
  await probe.unmount()
  return $.ui.mount({ ...spec, props, plugin })
}

async function ready($: any, clock: any) {
  await $.turn.complete(TURN)
  await clock.settle()
}

const row = async (ui: any, r: 'r1' | 'r2') => (await ui.find({ key: `ar-marquee-row-${r}` }))?.text as string | undefined
const cell = async (ui: any, name: string) => (await ui.find({ key: `ar-cell-${name}` }))?.text as string | undefined

// quota countdowns

test('quota: day, hour-minute and minute-second formats, and a missing window keeps the script text', SLOW, async ($, on) => {
  const { clock } = setup(on, {
    rateLimits: (now: number) => [
      { kind: 'five_hour', percentUsed: 41.6, resetsAt: new Date(now + 2 * HOUR + 5 * 60_000 + 30_000).toISOString() },
      { kind: 'seven_day', percentUsed: 27, resetsAt: new Date(now + 3 * 86_400_000 + 20 * HOUR + 60_000).toISOString() },
    ],
  })
  await ready($, clock)
  const ui = await mod($)
  expect(await row(ui, 'r1')).toMatch(/5h 已用 42%\(2h05m\) · 7d 已用 27%\(3d20h\)$/)
  await ui.unmount()
})

test('quota: a window with no reset time shows only the percent; a past reset shows 已重置', SLOW, async ($, on) => {
  const { clock } = setup(on, {
    rateLimits: (now: number) => [
      { kind: 'five_hour', percentUsed: 5 },
      { kind: 'seven_day', percentUsed: 27, resetsAt: new Date(now - 1000).toISOString() },
    ],
  })
  await ready($, clock)
  const ui = await mod($)
  expect(await row(ui, 'r1')).toMatch(/5h 已用 5% · 7d 已用 27%\(已重置\)$/)
  await ui.unmount()
})

test('quota: with no rate limits the script text stays', SLOW, async ($, on) => {
  const { clock } = setup(on, { rateLimits: [] })
  await ready($, clock)
  const ui = await mod($)
  expect(await row(ui, 'r1')).toMatch(/5h 已用 5%\(49m\) · 7d 已用 27%\(3d20h\)$/)
  await ui.unmount()
})

test('quota: the rate-limit figures reach the scorecard stdin', SLOW, async ($, on) => {
  const { clock, stdins } = setup(on)
  await ready($, clock)
  const sent = JSON.parse(stdins[0])
  expect(sent.session_id).toBe(SID)
  expect(sent.rate_limits.five_hour.used_percentage).toBe(5)
  expect(typeof sent.rate_limits.five_hour.resets_at).toBe('number')
})

// cache countdown

test('cache: a failing scorecard run drops the countdown instead of showing a false expiry', SLOW, async ($, on) => {
  const cfg: Cfg = { out: stats('Opus 5.5(1M) · proj · Cache 剩 0m20s/5m · context 已用 35%') }
  const { clock } = setup(on, cfg)
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'cache')).toBe('◷ 0m20s/5m')
  cfg.exitCode = 1
  await clock.advance(61_000)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: '◷ 已過期' })).toBeUndefined()
  expect(await cell(ui, 'cache')).toBe('◷ --')
  await ui.unmount()
})

test('cache: a scorecard that cannot start drops the countdown too, not only a failing exit', SLOW, async ($, on) => {
  const cfg: Cfg = { out: stats('Opus 5.5(1M) · proj · Cache 剩 0m20s/5m · context 已用 35%') }
  const { clock } = setup(on, cfg)
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'cache')).toBe('◷ 0m20s/5m')
  cfg.runFails = true
  await clock.advance(61_000)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: '◷ 已過期' })).toBeUndefined()
  expect(await cell(ui, 'cache')).toBe('◷ --')
  await ui.unmount()
})

test('cache: expired shows the inverse error cell', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats('Opus 5.5(1M) · proj · Cache 剩 0m20s/5m · context 已用 35%') })
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'cache')).toBe('◷ 0m20s/5m')
  await clock.advance(21_000)
  await clock.settle()
  const dead = await ui.find({ type: 'Text', text: '◷ 已過期' })
  expect(dead).toBeDefined()
  expect(dead!.props.backgroundColor).toBe('error')
  expect(dead!.props.bold).toBe(true)
  await ui.unmount()
})

test('cache: an hour-minute countdown ticks with the h/m format', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats('Opus 5.5(1M) · proj · Cache 剩 1h00m/1h · context 已用 35%') })
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'cache')).toBe('◷ 1h00m/1h')
  await clock.advance(5000)
  await clock.settle()
  expect(await cell(ui, 'cache')).toBe('◷ 59m55s/1h')
  await ui.unmount()
})

test('cache: a row with an elapsed-time piece in the cache slot shows no cache cell', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats('Opus 5.5(1M) · proj · 1h05m · context 已用 35%') })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ key: 'ar-cell-cache' })).toBeUndefined()
  await clock.advance(5000)
  await clock.settle()
  expect(await ui.find({ key: 'ar-cell-cache' })).toBeUndefined()
  await ui.unmount()
})

// model and effort

test('model cell shows the model name without an effort when settings have none', SLOW, async ($, on) => {
  const { clock } = setup(on, { settings: () => ({ value: {} }) })
  await ready($, clock)
  const ui = await mod($)
  expect((await ui.find({ key: 'ar-cell-model' })).text).toBe('Opus 5.5(1M)')
  await ui.unmount()
})

test('missing settings (read throws) leave the mod drawn without an effort', SLOW, async ($, on) => {
  const { clock } = setup(on, {
    settings: () => {
      throw new Error('no settings')
    },
  })
  await ready($, clock)
  const ui = await mod($)
  expect((await ui.find({ key: 'ar-cell-model' })).text).toBe('Opus 5.5(1M)')
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:code-finder（haiku）' })).toBeDefined()
  await ui.unmount()
})

test('turn.step reports the effort the request goes out with', SLOW, async ($, on) => {
  const { clock } = setup(on, { settings: () => ({ value: {} }) })
  on('turn.step', async function* () {
    return { turnId: 't', index: 0, answer: '', toolUses: [] } as any
  })
  await ready($, clock)
  for await (const _ of $.turn.step({ turnId: 't', index: 0, model: 'opus', effort: 'xhigh', messageCount: 1 })) void _
  const ui = await mod($)
  expect((await ui.find({ key: 'ar-cell-model' })).text).toBe('Opus 5.5(1M) xhigh')
  await ui.unmount()
})

test('the model picker lists the models, closes with the cross and ignores a missing model row', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  expect(await ui.find({ type: 'Button', text: 'sonnet' })).toBeDefined()
  await ui.press({ key: 'ar-menu-close' })
  await clock.settle()
  expect(await ui.find({ key: 'ar-menu' })).toBeUndefined()
  await ui.unmount()
})

test('the model picker with no model row opens with no model buttons', SLOW, async ($, on) => {
  const { clock } = setup(on, { config: [] })
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  expect(await ui.find({ key: 'ar-menu' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: 'sonnet' })).toBeUndefined()
  await ui.unmount()
})

test('picking a model runs the scorecard again at once', SLOW, async ($, on) => {
  const { clock, runs } = setup(on)
  await ready($, clock)
  const count = () => runs.filter(a => a[0].endsWith('/bin/ap')).length
  const before = count()
  const ui = await mod($)
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  await ui.press({ key: 'ar-model-sonnet' })
  await clock.settle()
  expect(count()).toBe(before + 1)
  await ui.unmount()
})

test('the cwd cell opens the working directory', SLOW, async ($, on) => {
  const { clock, runs } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-item-cwd' })
  await clock.settle()
  expect(runs).toContainEqual(['open', '/tmp/proj'])
  await ui.unmount()
})

test('quota cells are plain text, not buttons', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($)
    expect((await ui.find({ key: 'ar-cell-5h' })).text).toMatch(/^5h 已用 5%/)
  expect(await ui.find({ type: 'Button', text: /^5h/ })).toBeUndefined()
  await ui.unmount()
})

// context, cache, delegation: row 2

for (const [pct, gauge] of [[0, '▱▱▱▱▱'], [1, '▰▱▱▱▱'], [100, '▰▰▰▰▰'], [101, '▰▰▰▰▰']] as const) {
  test(`context gauge at ${pct} percent is ${gauge}`, SLOW, async ($, on) => {
    const { clock } = setup(on, { out: stats(`Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 ${pct}%`) })
    await ready($, clock)
    const ui = await mod($)
    expect(await cell(ui, 'ctx')).toBe(`${gauge} ${pct}% +86k`)
    await ui.unmount()
  })
}

test('a context piece that is not "context 已用 N%" is shown as the script wrote it', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats('Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 未知') })
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'ctx')).toBe('context 未知 +86k')
  await ui.unmount()
})

test('before the first turn, "context 已用 --" is an empty gauge, without the word context', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats('Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 --') })
  await ready($, clock)
  const ui = await mod($)
  expect(await cell(ui, 'ctx')).toBe('▱▱▱▱▱ -- +86k')
  await ui.unmount()
})

test('the subagent line becomes an item with its note in parentheses', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats(ROW1, '本回合已委派 code-finder（haiku） · 別名 reviewer', SESSION) })
  await ready($, clock)
  const ui = await mod($)
  expect((await ui.find({ key: 'ar-cell-delegate' })).text).toBe('已委派 code-finder（haiku）（別名 reviewer）')
  await ui.unmount()
})

test('no subagent line: no delegation cell', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats(ROW1, SESSION) })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ key: 'ar-cell-delegate' })).toBeUndefined()
  expect(await cell(ui, 'ctx')).toBe('▰▰▱▱▱ 35% +86k')
  await ui.unmount()
})

test('a session line that does not start with the session id gives no turn label and no burst', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats(ROW1, SUBAGENT, 'ffffffff #7 · 花費 $1.00 · 本回合 context +86k（平常 +6k）') })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ key: 'ar-turn' })).toBeUndefined()
  expect(await row(ui, 'r2')).toBe('3f8a1c2e：▰▰▱▱▱ 35% · ◷ 59m59s/1h · 已委派 agent-perform:code-finder（haiku）')
  await ui.unmount()
})

test('the session label links to the dashboard and the turn label to its pipeline page', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  expect((await ui.find({ key: 'ar-sid' })).text).toBe('3f8a1c2e')
  expect((await ui.find({ key: 'ar-turn' })).text).toBe('› #115')
  await ui.unmount()
})

test('the cache cell opens the cache-break page', SLOW, async ($, on) => {
  const { clock, runs } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-item-cache' })
  await clock.settle()
  expect(runs).toContainEqual(['open', 'http://localhost:8787/context?focus=cachebreak'])
  await ui.unmount()
})

test('cache and context alerts light up as warnings; the burst alert is held after a new prompt', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats() + '\nalert:ctx,cache,burst' })
  await ready($, clock)
  await $.prompt.submit({ text: 'hi' })
  const ui = await mod($, 400)
  const warn = (await ui.findAll({ type: 'Text' })).filter((t: any) => t.props.color === 'warning').map((t: any) => t.text)
  expect(warn).toEqual(['▰▰▱▱▱ 35%', '◷ 59m59s/1h'])
  await ui.unmount()
})

test('ANSI colour codes in the script output are stripped', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: stats().replace('本回合已委派', '\x1b[31m本回合已委派\x1b[0m') })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:code-finder（haiku）' })).toBeDefined()
  await ui.unmount()
})

// error paths

test('script exit code != 0 draws no rows', SLOW, async ($, on) => {
  const { clock } = setup(on, { exitCode: 1 })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ key: 'ar-marquee-r1' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: 'base' })).toBeDefined()
  await ui.unmount()
})

test('empty script output draws no rows', SLOW, async ($, on) => {
  const { clock } = setup(on, { out: '\n  \n' })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ key: 'ar-marquee-r1' })).toBeUndefined()
  await ui.unmount()
})

test('a failing fs.write does not stop the mod', SLOW, async ($, on) => {
  const { clock } = setup(on, {
    fsWrite: () => {
      throw new Error('disk full')
    },
  })
  await ready($, clock)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:code-finder（haiku）' })).toBeDefined()
  await ui.unmount()
})

test('null usage percent: scorecard gets a null remaining percentage and no hotkey row appears', SLOW, async ($, on) => {
  const { clock, stdins } = setup(on, { percent: undefined })
  await ready($, clock)
  expect(JSON.parse(stdins[0]).context_window.remaining_percentage).toBeNull()
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  await ui.unmount()
})

test('remaining percentage is 100 minus the fill', SLOW, async ($, on) => {
  const { clock, stdins } = setup(on, { percent: 35 })
  await ready($, clock)
  const sent = JSON.parse(stdins[0])
  expect(sent.context_window).toEqual({ context_window_size: 1_000_000, remaining_percentage: 65 })
  expect(sent.model.display_name).toBe('Opus 5.5 (1M context)')
  expect(sent.workspace.current_dir).toBe('/tmp/proj')
})

test('a survey prompt hides the mod', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($, 400, { ...PROPS, hasSurvey: true })
  expect(await ui.find({ key: 'ar-marquee-r1' })).toBeUndefined()
  await ui.unmount()
})

test('the version shows in the corner; a failing plugin.json read shows none', SLOW, async ($, on) => {
  const a = setup(on, { version: '9.9.9' })
  await ready($, a.clock)
  const ui = await mod($)
  await a.clock.settle()
  expect(await ui.find({ type: 'Text', text: 'v9.9.9' })).toBeDefined()
  expect(a.runs.some(r => r[0] === 'cat' && r[1].endsWith('/.claude-plugin/plugin.json'))).toBe(true)
  await ui.unmount()
})

test('no version when plugin.json cannot be read', SLOW, async ($, on) => {
  const { clock } = setup(on, { version: null })
  await ready($, clock)
  const ui = await mod($)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /^v\d/ })).toBeUndefined()
  await ui.unmount()
})

// context hotkey row

test('hotkey row: /clear runs the clear command and hides the row', SLOW, async ($, on) => {
  const { clock, log } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-clear' })
  await clock.settle()
  expect(log).toEqual(['clear'])
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeUndefined()
  await ui.unmount()
})

test('hotkey row: a failing action still hides the row', SLOW, async ($, on) => {
  const { clock } = setup(on, { compactFails: true })
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-clear' })
  await clock.settle()
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  await ui.unmount()
})

test('hotkey row: comes back after a dismissal once the fill grew 10 points', SLOW, async ($, on) => {
  const { clock, state } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-close' })
  await clock.settle()
  state.percent = 44
  await ready($, clock)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  state.percent = 45
  await ready($, clock)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  await ui.unmount()
})

test('hotkey row: dropping below 30 percent clears a dismissal', SLOW, async ($, on) => {
  const { clock, state } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-close' })
  await clock.settle()
  state.percent = 20
  await ready($, clock)
  state.percent = 36
  await ready($, clock)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  await ui.unmount()
})

test('hotkey row: appears at exactly 30 percent and not at 29', SLOW, async ($, on) => {
  const { clock, state } = setup(on, { percent: 29 })
  await ready($, clock)
  let ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeUndefined()
  await ui.unmount()
  state.percent = 30
  await ready($, clock)
  ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeDefined()
  await ui.unmount()
})

// session.measure

test('session.measure: new rate-limit figures redraw the quota cell at once', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($)
  const resetsAt = new Date(clock.now() + 90 * 60_000).toISOString()
  await $.session.measure({
    context: { percent: 35, window: 1_000_000 },
    rateLimits: [{ kind: 'five_hour', percentUsed: 80, resetsAt }],
    changed: ['rateLimits'],
  })
  await clock.settle()
  expect(await row(ui, 'r1')).toMatch(/5h 已用 80%\(1h30m\)/)
  await ui.unmount()
})

test('session.measure: a context-only change leaves the quota alone and syncs the hotkey row', SLOW, async ($, on) => {
  const { clock, state } = setup(on, { percent: 20 })
  await ready($, clock)
  state.percent = 50
  await $.session.measure({ context: { percent: 50, window: 1_000_000 }, rateLimits: [], changed: ['context'] })
  await clock.settle()
  const ui = await mod($)
  expect(await row(ui, 'r1')).toMatch(/5h 已用 5%/)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  await ui.unmount()
})

// refresh throttle

test('a new prompt refreshes the scorecard again after 1.5 s; a refresh inside 30 s waits for the window', SLOW, async ($, on) => {
  const { clock, runs } = setup(on)
  await ready($, clock)
  const count = () => runs.filter(a => a[0].endsWith('/bin/ap')).length
  const before = count()
  await $.prompt.submit({ text: 'hi' })
  await clock.advance(1500)
  await clock.settle()
  expect(count()).toBe(before + 1)
  const ui = await mod($)
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  await ui.press({ key: 'ar-model-sonnet' })
  await clock.settle()
  expect(count()).toBe(before + 2)
  await ui.unmount()
})

test('a refresh asked inside the throttle window runs once when it ends', SLOW, async ($, on) => {
  const { clock, runs } = setup(on)
  await $.turn.complete(TURN)
  await clock.settle()
  const count = () => runs.filter(a => a[0].endsWith('/bin/ap')).length
  const before = count()
  await $.session.measure({ context: { percent: 35, window: 1_000_000 }, rateLimits: [], changed: ['cost'] })
  await $.session.measure({ context: { percent: 35, window: 1_000_000 }, rateLimits: [], changed: ['cost'] })
  await clock.settle()
  expect(count()).toBe(before)
  await clock.advance(30_000)
  await clock.settle()
  expect(count()).toBeGreaterThanOrEqual(before + 1)
})

// pager

test('pager: the previous arrow goes back a page and the first page has a dimmed arrow', SLOW, async ($, on) => {
  const { clock } = setup(on)
  await ready($, clock)
  const ui = await mod($, 40)
  expect(await ui.find({ type: 'Text', text: '◀' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: '◀' })).toBeUndefined()
  await ui.press({ key: 'ar-next-r2' })
  await clock.settle()
  expect(await ui.find({ type: 'Button', key: 'ar-prev-r2' })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /^2\/\d$/ })).toBeDefined()
  await ui.press({ key: 'ar-prev-r2' })
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /^1\/\d$/ })).toBeDefined()
  await ui.unmount()
})

// autoCompact

const AUTO = { ...SLOW, options: { autoCompact: true } }

test('autoCompact: exactly 60 percent compacts after the idle hour', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 60 })
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual(['recap', 'compact:goal and next step'])
})

test('autoCompact: 59 percent does nothing', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 59 })
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual([])
})

test('autoCompact: unknown usage percent does nothing', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: undefined })
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual([])
})

test('autoCompact: a new prompt inside the hour cancels the compact', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 65 })
  await $.turn.complete(TURN)
  await clock.advance(HOUR - 1000)
  await $.prompt.submit({ text: 'back' })
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual([])
})

test('autoCompact: a later turn restarts the idle hour', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 65 })
  await $.turn.complete(TURN)
  await clock.advance(HOUR - 1000)
  await $.turn.complete(TURN)
  await clock.advance(HOUR - 1000)
  expect(log).toEqual([])
  await clock.advance(1000)
  await clock.settle()
  expect(log).toEqual(['recap', 'compact:goal and next step'])
})

test('autoCompact: a context that drops under 60 percent before the next turn ends cancels it', AUTO, async ($, on) => {
  const { clock, log, state } = setup(on, { percent: 65 })
  await $.turn.complete(TURN)
  state.percent = 10
  await clock.advance(HOUR - 1)
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual([])
})

test('autoCompact: an empty recap falls back to a plain compact', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 65, commandText: '   ' })
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual(['recap', 'compact:'])
})

test('autoCompact: the recap is trimmed before it is the instructions', AUTO, async ($, on) => {
  const { clock, log } = setup(on, { percent: 65, commandText: '\n  goal  \n' })
  await $.turn.complete(TURN)
  await clock.advance(HOUR)
  await clock.settle()
  expect(log).toEqual(['recap', 'compact:goal'])
})

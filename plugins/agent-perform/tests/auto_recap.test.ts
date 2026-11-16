import { expect, mock, test } from 'claude-code/testing'

// The hour-long idle tests run near the 5 s default when the machine is busy.
const SLOW = { timeoutMs: 20_000 }

const TURN = { turnId: 't', answer: 'done', durationMs: 1, isAborted: false, usage: null }
const HOUR = 60 * 60_000
const STATS = [
  'Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 35% · 5h 已用 5%(49m) · 7d 已用 27%(3d20h)',
  '本回合已委派 agent-perform:code-finder（haiku）',
  '3f8a1c2e #115 · 花費 $28.51 · 浪費 $0.42 · 本回合 context +86k（平常 +6k）',
].join('\n')

let statsOut = STATS

function setup(on: any, percent: number) {
  const clock = mock.clock(on)
  const log: string[] = []
  const runs: string[][] = []
  on('turn.complete', () => ({ text: '' }))
  on('prompt.submit', ($: any, e: any) => ({ text: e.text }))
  on('ui.render', () => ({ type: 'Text', props: {}, children: ['base'] }))
  on('session.usage', () => ({ value: { context: { percent, window: 1_000_000 }, rateLimits: [{ kind: 'five_hour', percentUsed: 5, resetsAt: new Date(clock.now() + 49 * 60_000).toISOString() }], cost: {} } }))
  on('session.id', () => ({ value: '3f8a1c2e-5b7d-4e90-9a6c-1d2e3f4a5b6c' }))
  on('session.model', () => ({ value: 'Opus 5.5 (1M context)' }))
  on('session.cwd', () => ({ value: '/tmp/proj' }))
  on('fs.write', () => ({ value: undefined }))
  on('settings.read', () => ({ value: { effortLevel: 'medium' } }))
  on('process.run', ($: any, e: any) => {
    const argv: string[] = e.argv ?? e.command
    runs.push(argv)
    if (argv[0] === 'open') return { value: { exitCode: 0, stdout: '', stderr: '' } }
    return { value: { exitCode: 0, stdout: statsOut, stderr: '' } }
  })
  on('config.list', () => ({ value: [{ key: 'model', label: 'Model', kind: 'choice', value: 'opus[1m]', options: ['opus[1m]', 'sonnet'] }] }))
  on('config.set', ($: any, e: any) => {
    log.push(`config:${e.key}=${e.value}`)
    return { value: e.value }
  })
  on('command.run', ($: any, e: any) => {
    log.push(e.args ? `${e.command} ${e.args}` : e.command)
    return { text: e.command === 'recap' ? 'goal and next step' : '' }
  })
  on('session.compact', ($: any, e: any) => {
    log.push(`compact:${e.instructions ?? ''}`)
    return { skip: 'test' }
  })
  return { clock, log, runs }
}

const PROPS = { hasSurvey: false, isWorking: false, maxRows: 10 }

// The plugin's own name (it differs between copies) is on its pressable elements.
const pluginOf = (n: any): string | undefined => n?.press?.plugin ?? n?.children?.map?.(pluginOf).find(Boolean)

async function mod($: any, columns = 400) {
  const spec = { component: 'AbovePrompt', surface: 'terminal', viewport: { columns, rows: 30 }, props: PROPS }
  const probe = await $.ui.mount({ ...spec, plugin: '' })
  return $.ui.mount({ ...spec, plugin: pluginOf(await probe.drawn()) })
}

async function idle($: any, clock: any, ms = 0) {
  await $.turn.complete(TURN)
  await clock.advance(ms)
  await clock.settle()
}

test('stats lines render from the script output', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:code-finder（haiku）' })).toBeDefined()
  expect((await ui.find({ key: 'ar-item-ctx' }))?.text).toBe('▰▰▱▱▱ 35% +86k')
  expect(await ui.find({ text: /花費|浪費/ })).toBeUndefined()
  await ui.unmount()
})

test('countdowns tick every second without running the script again', SLOW, async ($, on) => {
  const { clock, runs } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  const scriptRuns = () => runs.filter(a => a[0].endsWith('/bin/ap')).length
  const before = scriptRuns()
  expect(await ui.find({ type: 'Text', text: /5h 已用 5%\(49m00s\)/ })).toBeDefined()
  await clock.advance(1000)
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: /5h 已用 5%\(48m59s\)/ })).toBeDefined()
  expect(scriptRuns()).toBe(before)
  await ui.unmount()
})

test('the data refresh runs again after 30 s with new script output', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  statsOut = STATS.replace('code-finder', 'reviewer')
  await clock.advance(30_000)
  await clock.settle()
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:reviewer（haiku）' })).toBeDefined()
  await ui.unmount()
  statsOut = STATS
})

test('suggestion row has /clear, /compact and /recap', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: '/compact' })).toBeDefined()
  expect(await ui.find({ type: 'Button', text: '/compact with /recap' })).toBeUndefined()
  await ui.unmount()
})

test('mod appears as soon as the turn ends at 35%', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: '3:' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /^▰/ })).toBeUndefined()
  expect(await ui.find({ type: 'Button', text: '/compact' })).toBeDefined()
  expect(log).toEqual([])
  await ui.unmount()
})

test('/recap runs the recap and hides the mod', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-recap' })
  await clock.settle()
  expect(log).toEqual(['recap'])
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  await ui.unmount()
})

test('/compact compacts with no instructions', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-compact' })
  await clock.settle()
  expect(log).toEqual(['compact:'])
  await ui.unmount()
})

test('close hides the mod and runs nothing', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-close' })
  await clock.settle()
  expect(log).toEqual([])
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  await ui.unmount()
})

test('the hotkey row stays while the next turn runs', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await idle($, clock)
  await $.prompt.submit({ text: 'hi' })
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  for (const k of ['ar-clear', 'ar-compact', 'ar-recap']) expect(JSON.stringify(await ui.find({ key: k }))).not.toContain('hotkey')
  await ui.unmount()
})

test('closing the hotkey row keeps it away until context grows 10 points', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await idle($, clock)
  const ui = await mod($)
  await ui.press({ key: 'ar-close' })
  await clock.settle()
  await $.turn.complete(TURN)
  await clock.settle()
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  await ui.unmount()
})

test('no mod below 30%', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 25)
  await idle($, clock, HOUR)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeUndefined()
  expect(log).toEqual([])
  await ui.unmount()
})

test('mod stays shown above 40%', SLOW, async ($, on) => {
  const { clock } = setup(on, 55)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/recap' })).toBeDefined()
  await ui.unmount()
})

test('autoCompact is off by default: nothing at 60% after an hour', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 65)
  await idle($, clock, HOUR)
  expect(log).toEqual([])
})

test('Mod items open their pages', SLOW, async ($, on) => {
  const { clock, runs } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  for (const key of ['ar-item-delegate', 'ar-item-ctx', 'ar-item-cache']) await ui.press({ key })
  await clock.settle()
  expect(runs).toContainEqual(['open', 'http://localhost:8787/delegate'])
  expect(runs).toContainEqual(['open', 'http://localhost:8787/pipeline?session=3f8a1c2e&turn=115'])
  expect(runs).toContainEqual(['open', 'http://localhost:8787/context?focus=cachebreak'])
  await ui.unmount()
})

// A whole marquee row as drawn, its label included: r1 is the environment, r2 the session and turn.
async function rowText(ui: any, row: 'r1' | 'r2'): Promise<string> {
  return (await ui.find({ key: `ar-marquee-row-${row}` })).text
}

test('wide mod shows every item with no page buttons', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 400)
  const joined = `${await rowText(ui, 'r1')} ${await rowText(ui, 'r2')}`
  for (const t of ['Opus 5.5(1M) medium', 'proj', '5h', '7d', '▰▰▱▱▱ 35%', '+86k', '◷ 59m59s/1h', '已委派']) expect(joined).toContain(t)
  expect(joined).not.toMatch(/[◀▶]|花費|浪費|context|Cache/)
  expect(await ui.find({ key: 'ar-next-r2' })).toBeUndefined()
  await ui.unmount()
})

const cellsOf = (t: string) => [...t].reduce((n, ch) => n + (/[\u2e80-\ua4cf\uff00-\uff60]/.test(ch) ? 2 : 1), 0)
const cell = async (ui: any, name: string) => (await ui.find({ key: `ar-cell-${name}` })).text as string
// The row-2 label `3f8a1c2e › #115：` and the band's own margins.
const LABEL = cellsOf('3f8a1c2e › #115：')
const usable = (columns: number) => columns - 5 - 2 - LABEL

test('narrow mod scrolls only the long items inside their own cells', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 80)
  expect(await ui.find({ key: 'ar-next-r2' })).toBeUndefined()
  const before = { cache: await cell(ui, 'cache'), delegate: await cell(ui, 'delegate') }
  expect(cellsOf(before.cache)).toBeLessThan(cellsOf('◷ 59m59s/1h'))
  expect(cellsOf(before.delegate)).toBeLessThan(cellsOf('已委派 agent-perform:code-finder（haiku）'))
  expect(cellsOf(before.delegate)).toBeGreaterThanOrEqual(10)
  expect(await cell(ui, 'ctx')).toBe('▰▰▱▱▱ 35% +86k')
  await clock.advance(2500)
  await clock.settle()
  expect(await cell(ui, 'delegate')).not.toBe(before.delegate)
  expect(await cell(ui, 'cache')).not.toBe(before.cache)
  expect(await cell(ui, 'model')).toBe('Opus 5.5(1M) medium')
  expect(cellsOf(await cell(ui, 'delegate'))).toBe(cellsOf(before.delegate))
  await ui.unmount()
})

test('every cell is at least 10 wide and the row fits the width', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  for (const columns of [70, 80, 90]) {
    const ui = await mod($, columns)
    const parts = [await cell(ui, 'ctx'), await cell(ui, 'cache'), await cell(ui, 'delegate')]
    for (const t of parts) expect(cellsOf(t)).toBeGreaterThanOrEqual(10)
    expect(parts.reduce((n, t) => n + cellsOf(t), 0) + 2 * 3).toBeLessThanOrEqual(usable(columns))
    await ui.unmount()
  }
})

test('a very narrow mod falls back to pages with the arrows', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 40)
  const first = await rowText(ui, 'r2')
  expect(first).toContain('▰▰▱▱▱ 35% +86k')
  expect(first).not.toContain('已委派')
  expect(await ui.find({ type: 'Text', text: '◀' })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /^1\/\d$/ })).toBeDefined()
  let guard = 0
  while (await ui.find({ type: 'Button', key: 'ar-next-r2' })) {
    await ui.press({ key: 'ar-next-r2' })
    await clock.settle()
    expect(++guard).toBeLessThan(10)
  }
  expect(await rowText(ui, 'r2')).toContain('已委派')
  await ui.unmount()
})

const warnings = async (ui: any) => (await ui.findAll({ type: 'Text' })).filter((t: any) => t.props.color === 'warning').map((t: any) => t.text)

test('a burst alert colours only the +86k and the context stays whole', SLOW, async ($, on) => {
  statsOut = STATS + '\nalert:burst'
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 80)
  expect(await warnings(ui)).toEqual(['+86k'])
  await clock.advance(5000)
  await clock.settle()
  expect(await cell(ui, 'ctx')).toBe('▰▰▱▱▱ 35% +86k')
  await ui.unmount()
  statsOut = STATS
})

test('the next prompt holds the burst colour until its turn ends', SLOW, async ($, on) => {
  statsOut = STATS + '\nalert:burst'
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  await $.prompt.submit({ text: 'hi' })
  const ui = await mod($, 400)
  expect(await warnings(ui)).toEqual([])
  await $.turn.complete(TURN)
  await clock.settle()
  expect(await warnings(ui)).toEqual(['+86k'])
  await ui.unmount()
  statsOut = STATS
})

test('a context alert stays lit on the gauge while the next turn runs', SLOW, async ($, on) => {
  statsOut = STATS + '\nalert:ctx'
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  await $.prompt.submit({ text: 'hi' })
  const ui = await mod($, 400)
  expect(await warnings(ui)).toEqual(['▰▰▱▱▱ 35%'])
  await ui.unmount()
  statsOut = STATS
})

test('the session id and › #turn open the session and turn pages', SLOW, async ($, on) => {
  const { clock, runs } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 400)
  await ui.press({ key: 'ar-sid' })
  await ui.press({ key: 'ar-turn' })
  await clock.settle()
  expect(runs).toContainEqual(['open', 'http://localhost:8787/?session=3f8a1c2e'])
  expect(runs).toContainEqual(['open', 'http://localhost:8787/pipeline?session=3f8a1c2e&turn=115'])
  await ui.unmount()
})

test('pressing the model opens the picker; picking a model sets it', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: 'sonnet' })).toBeUndefined()
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: '● opus[1m]' })).toBeDefined()
  await ui.press({ key: 'ar-model-sonnet' })
  await clock.settle()
  expect(log).toEqual(['config:model=sonnet'])
  expect(await ui.find({ type: 'Button', text: 'sonnet' })).toBeUndefined()
  await ui.unmount()
})

test('picking an effort runs /effort and shows it beside the model', SLOW, async ($, on) => {
  const { clock, log } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  await ui.press({ key: 'ar-effort-high' })
  await clock.settle()
  expect(log).toEqual(['effort high'])
  expect(await ui.find({ type: 'Button', text: 'Opus 5.5(1M) high' })).toBeDefined()
  await ui.unmount()
})

test('the effort comes from settings before any request reports it', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: 'Opus 5.5(1M) medium' })).toBeDefined()
  await ui.press({ key: 'ar-item-model' })
  await clock.settle()
  expect(await ui.find({ type: 'Text', text: '● medium' })).toBeDefined()
  await ui.unmount()
})

test('rows: environment on r1; id, turn, context, Cache and delegation on r2; no middle dot inside an item', SLOW, async ($, on) => {
  const { clock } = setup(on, 35)
  await $.turn.complete(TURN)
  await clock.settle()
  const ui = await mod($, 200)
  expect(await rowText(ui, 'r1')).toMatch(/5h 已用 5%\(49m00s\) · 7d 已用 27%\(3d20h\)$/)
  expect(await rowText(ui, 'r2')).toBe('3f8a1c2e › #115：▰▰▱▱▱ 35% +86k · ◷ 59m59s/1h · 已委派 agent-perform:code-finder（haiku）')
  for (const r of ['r1', 'r2']) for (const item of (await ui.find({ key: `ar-marquee-${r}` })).text.split(' · ')) expect(item).not.toContain('·')
  await ui.unmount()
})

test('the hotkey row shows only when its trigger holds, with no duplicate number labels', SLOW, async ($, on) => {
  const { clock } = setup(on, 25)
  await idle($, clock)
  const ui = await mod($)
  expect(await ui.find({ type: 'Button', text: '/clear' })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /^\d:$/ })).toBeUndefined()
  await ui.unmount()
})

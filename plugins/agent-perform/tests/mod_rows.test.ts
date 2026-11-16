import { expect, mock, test } from 'claude-code/testing'

// Mod rendering for the scorecard shapes bin/ap prints: the subagent line only
// exists after a delegation, and row 2 leaves the money parts to the dashboard.

const SLOW = { timeoutMs: 20_000 }
const TURN = { turnId: 't', answer: 'done', durationMs: 1, isAborted: false, usage: null }
const LIVE = 'Opus 5.5(1M) · proj · Cache 剩 59m59s/1h · context 已用 35% · 5h 已用 5%(49m) · 7d 已用 27%(3d20h)'
const SID = '3f8a1c2e'

let statsOut = ''

function setup(on: any) {
  const clock = mock.clock(on)
  on('turn.complete', () => ({ text: '' }))
  on('prompt.submit', ($: any, e: any) => ({ text: e.text }))
  on('ui.render', () => ({ type: 'Text', props: {}, children: ['base'] }))
  on('session.usage', () => ({ value: { context: { percent: 35, window: 1_000_000 }, rateLimits: [], cost: {} } }))
  on('session.id', () => ({ value: `${SID}-723d-4199-8c70-8557ad6bf473` }))
  on('session.model', () => ({ value: 'Opus 5.5 (1M context)' }))
  on('session.cwd', () => ({ value: '/tmp/proj' }))
  on('fs.write', () => ({ value: undefined }))
  on('settings.read', () => ({ value: { effortLevel: 'medium' } }))
  on('process.run', ($: any, e: any) => {
    const argv: string[] = e.argv ?? e.command
    if (argv[0] === 'open') return { value: { exitCode: 0, stdout: '', stderr: '' } }
    return { value: { exitCode: 0, stdout: statsOut, stderr: '' } }
  })
  on('config.list', () => ({ value: [] }))
  on('command.run', () => ({ text: '' }))
  on('session.compact', () => ({ skip: 'test' }))
  return clock
}

const PROPS = { hasSurvey: false, isWorking: false, maxRows: 10 }
const pluginOf = (n: any): string | undefined => n?.press?.plugin ?? n?.children?.map?.(pluginOf).find(Boolean)

async function draw($: any, on: any, stats: string[]) {
  statsOut = [LIVE, ...stats].join('\n')
  const clock = setup(on)
  await $.turn.complete(TURN)
  await clock.settle()
  const spec = { component: 'AbovePrompt', surface: 'terminal', viewport: { columns: 400, rows: 30 }, props: PROPS }
  const probe = await $.ui.mount({ ...spec, plugin: '' })
  const ui = await $.ui.mount({ ...spec, plugin: pluginOf(await probe.drawn()) })
  return { ui, drawn: JSON.stringify(await ui.drawn()) }
}

const cellKeys = (drawn: string) => [...drawn.matchAll(/ar-cell-(\w+)/g)].map(m => m[1])

test('whatever the session line carries, row 2 never shows 花費 or 浪費', SLOW, async ($, on) => {
  const { ui, drawn } = await draw($, on, [`${SID} #3 · 花費 $1.50 · 浪費 $0.42`])
  expect(drawn).not.toMatch(/花費|浪費/)
  expect(cellKeys(drawn)).not.toContain('cost')
  expect((await ui.find({ key: 'ar-turn' }))?.text).toBe('› #3')
  await ui.unmount()
})

test('no 本回合 line at all: no delegate cell, no burst, nothing empty', SLOW, async ($, on) => {
  const { ui, drawn } = await draw($, on, [`${SID} #3 · 花費 $1.50`])
  expect(cellKeys(drawn).sort()).toEqual(['5h', '7d', 'cache', 'ctx', 'cwd', 'model'].sort())
  expect((await ui.find({ key: 'ar-cell-ctx' }))?.text).toBe('▰▰▱▱▱ 35%')
  expect(drawn).not.toContain('由主對話處理')
  expect(drawn).not.toMatch(/"(text|children)":\s*\[?""/)
  await ui.unmount()
})

test('a subagent launched this turn: the 本回合已委派 line becomes the delegate cell, the burst follows the gauge', SLOW, async ($, on) => {
  const { ui, drawn } = await draw($, on, [
    '本回合已委派 agent-perform:implementer（sonnet）',
    `${SID} #3 · 花費 $1.50 · 本回合 context +86k（平常 +6k）`,
  ])
  expect(await ui.find({ type: 'Button', text: '已委派 agent-perform:implementer（sonnet）' })).toBeDefined()
  expect((await ui.find({ key: 'ar-cell-ctx' }))?.text).toBe('▰▰▱▱▱ 35% +86k')
  expect(drawn).not.toContain('由主對話處理')
  await ui.unmount()
})

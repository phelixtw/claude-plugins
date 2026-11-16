import { expect, mock, test } from 'claude-code/testing'

// The hour-long idle tests run near the 5 s default when the machine is busy.
const SLOW = { timeoutMs: 20_000 }

const TURN = { turnId: 't', answer: 'done', durationMs: 1, isAborted: false, usage: null }
const HOUR = 60 * 60_000

test('autoCompact on: compacts with the recap after an idle hour at 65%', { ...SLOW, options: { autoCompact: true } }, async ($, on) => {
  const clock = mock.clock(on)
  const log: string[] = []
  on('turn.complete', () => ({ text: '' }))
  on('session.usage', () => ({ value: { context: { percent: 65, window: 1_000_000 }, rateLimits: {}, cost: {} } }))
  on('command.run', ($: any, e: any) => {
    log.push(e.command)
    return { text: 'goal and next step' }
  })
  on('session.compact', ($: any, e: any) => {
    log.push(`compact:${e.instructions ?? ''}`)
    return { skip: 'test' }
  })
  await $.turn.complete(TURN)
  await clock.advance(HOUR - 1)
  expect(log).toEqual([])
  await clock.advance(1)
  await clock.settle()
  expect(log).toEqual(['recap', 'compact:goal and next step'])
})

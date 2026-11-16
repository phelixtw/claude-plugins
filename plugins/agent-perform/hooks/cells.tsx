import type { EngineInterface, RenderNode } from 'claude-code'

// The AbovePrompt surface's element constructors, as `$.ui.resolve(e)` hands them over.
export type Els = { Box: any; Button: any; Text: any }

export type Tone = 'plain' | 'dim' | 'warning' | 'dire'
export type Part = { text: string; tone: Tone }
// rigid never shrinks; scroll shrinks to MIN_CELL and marquees; elastic shrinks first, down to its head.
export type Cell =
  | { kind: 'rigid'; name: string; parts: readonly Part[]; go?: () => void }
  | { kind: 'scroll'; name: string; text: string; tone: Tone; go?: () => void }
  | { kind: 'elastic'; name: string; head: string; tail: string; tone: Tone; go: () => void }

export type Pager = { page: number; flip: (page: number) => void }

export const SEP = ' · '
export const STEP_MS = 500
const MIN_CELL = 10
const GAP = 3
const HOLD_STEPS = 3
// Each Button is its own hover group, so only the one under the pointer inverts.
export const hl = (scope: string) => ({ scope, inverse: true })

export function redraw($: EngineInterface) {
  void Promise.resolve($.ui.invalidate('ui.render')).catch(() => {})
}

// Terminal cells a character takes: CJK and full-width forms are two.
export function cells(ch: string): number {
  const c = ch.codePointAt(0)!
  const wide = (c >= 0x1100 && c <= 0x115f) || (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) ||
    (c >= 0xf900 && c <= 0xfaff) || (c >= 0xfe30 && c <= 0xfe6f) || (c >= 0xff00 && c <= 0xff60) || (c >= 0xffe0 && c <= 0xffe6)
  return wide ? 2 : 1
}
export const width = (s: string) => [...s].reduce((n, ch) => n + cells(ch), 0)

// At most w cells of s, ending in … when cut.
export function clip(s: string, w: number): string {
  if (width(s) <= w) return s
  if (w <= 0) return ''
  let out = ''
  let used = 0
  for (const ch of s) {
    if (used + cells(ch) > w - 1) break
    out += ch
    used += cells(ch)
  }
  return out + '…'
}

// s clipped and padded to exactly w cells; `end` pads on the left.
export function pad(s: string, w: number, end = false): string {
  const t = clip(s, w)
  const fill = ' '.repeat(Math.max(0, w - width(t)))
  return end ? fill + t : t + fill
}

// The w cells of `text` visible at this step: scrolls left one cell per step, holds 1.5 s with the tail in view, loops with a gap.
export function windowText(text: string, w: number, step: number): string {
  const total = width(text)
  const loop = total + GAP
  const maxOff = total - w
  const s = step % (loop + HOLD_STEPS)
  const off = s <= maxOff ? s : s <= maxOff + HOLD_STEPS ? maxOff : s - HOLD_STEPS
  let out = ''
  let pos = 0
  let used = 0
  for (const ch of [...text, ...' '.repeat(GAP), ...text]) {
    const c = cells(ch)
    if (pos >= off && used + c <= w) { out += ch; used += c }
    else if (pos < off && pos + c > off) { out += ' '; used++ }
    pos += c
    if (used >= w) break
  }
  return out
}

const isAlert = (t: Tone) => t === 'warning' || t === 'dire'

function natural(c: Cell): number {
  if (c.kind === 'rigid') return c.parts.reduce((n, p) => n + width(p.text), 0)
  if (c.kind === 'scroll') return width(c.text)
  return width(c.head) + (c.tail ? 1 + width(c.tail) : 0)
}

function least(c: Cell): number {
  if (c.kind === 'rigid' || (c.kind === 'scroll' && isAlert(c.tone))) return natural(c)
  if (c.kind === 'scroll') return Math.min(natural(c), MIN_CELL)
  return width(c.head)
}

// Cell widths for the row, or null when even the floors do not fit (the caller pages):
// elastic cells give way first, then scroll cells shrink proportionally; rigid and alerting cells never do.
export function allocate(cs: readonly Cell[], budget: number): number[] | null {
  const nat = cs.map(natural)
  const w = [...nat]
  let over = nat.reduce((a, b) => a + b, 0) - budget
  if (over <= 0) return nat
  cs.forEach((c, i) => {
    if (c.kind !== 'elastic' || over <= 0) return
    const give = Math.min(over, nat[i] - least(c))
    w[i] -= give
    over -= give
  })
  if (over <= 0) return w
  const flex = cs.flatMap((c, i) => (c.kind === 'scroll' && !isAlert(c.tone) ? [i] : []))
  const room = budget - w.reduce((a, n, i) => (flex.includes(i) ? a : a + n), 0)
  if (room < flex.reduce((a, i) => a + least(cs[i]), 0)) return null
  const ratio = room / flex.reduce((a, i) => a + nat[i], 0)
  for (const i of flex) w[i] = Math.min(nat[i], Math.max(MIN_CELL, Math.floor(nat[i] * ratio)))
  let left = flex.reduce((a, i) => a + w[i], 0) - room
  while (left-- > 0) {
    const i = flex.filter(k => w[k] > least(cs[k])).reduce((a, k) => (w[k] > w[a] ? k : a))
    w[i]--
  }
  return w
}

const rowWidth = (cs: readonly Cell[]) => cs.reduce((n, c) => n + natural(c), 0) + SEP.length * Math.max(0, cs.length - 1)

// Whole cells per page, filled in order; a cell wider than a page gets a page to itself.
export function paginate(cs: readonly Cell[], avail: number): Cell[][] {
  const pages: Cell[][] = []
  for (const c of cs) {
    const cur = pages[pages.length - 1]
    if (cur && rowWidth([...cur, c]) <= avail) cur.push(c)
    else pages.push([c])
  }
  return pages
}

function styleOf(t: Tone, pressable: boolean) {
  if (t === 'warning') return { color: 'warning' }
  if (t === 'dire') return { color: 'inverseText', backgroundColor: 'error', bold: true }
  return t === 'dim' || !pressable ? { dimColor: true } : null
}

function partsAt(c: Cell, w: number, now: number): readonly Part[] {
  if (c.kind === 'rigid') return c.parts
  if (c.kind === 'scroll') return [{ text: w < width(c.text) ? windowText(c.text, w, Math.floor(now / STEP_MS)) : c.text, tone: c.tone }]
  const room = w - width(c.head) - 1
  return [{ text: c.tail && room >= 2 ? `${c.head} ${clip(c.tail, room)}` : c.head, tone: c.tone }]
}

function cellBody(el: Els, c: Cell, w: number, now: number): RenderNode {
  const { Box, Button, Text } = el
  const key = `ar-item-${c.name}`
  const parts = partsAt(c, w, now)
  if (c.go) {
    return (
      <Button key={key} hover={hl(key)} plain onPress={c.go}>
        {parts.map(p => {
          const s = styleOf(p.tone, true)
          return s ? <Text {...s}>{p.text}</Text> : p.text
        })}
      </Button>
    )
  }
  if (parts.length === 1) return <Text key={key} {...styleOf(parts[0].tone, false)}>{parts[0].text}</Text>
  return <Box key={key} flexDirection="row">{parts.map(p => <Text {...styleOf(p.tone, false)}>{p.text}</Text>)}</Box>
}

// One row of fixed-width cells; a cell narrower than its text scrolls inside it.
// Only when even the floors do not fit, whole cells split into pages flipped with ◀ ▶.
export function marqueeRow(el: Els, row: { name: string; label?: readonly (RenderNode | null)[]; labelWidth?: number; cells: readonly Cell[]; avail: number; now: number; pager: Pager }): RenderNode | null {
  const { Box, Button, Text } = el
  const { name, cells: cs } = row
  if (cs.length === 0 && !row.label?.length) return null
  const avail = Math.max(10, row.avail - (row.labelWidth ?? 0))
  const cellW = allocate(cs, avail - SEP.length * Math.max(0, cs.length - 1))
  const whole = cellW !== null
  const pages = whole ? [cs] : paginate(cs, avail - (width(`${cs.length}/${cs.length}`) + 6))
  const cur = Math.min(row.pager.page, pages.length - 1)
  const nav = (key: string, label: string, enabled: boolean, to: number) => enabled
    ? <Button key={key} hover={hl(key)} plain label={label} onPress={() => row.pager.flip(to)} />
    : <Text key={key} dimColor>{label}</Text>
  const shown = pages[cur] ?? []
  return (
    <Box key={`ar-marquee-row-${name}`} flexDirection="row" flexWrap="nowrap" height={1} overflow="hidden">
      {row.label ?? null}
      <Box flexDirection="row" flexWrap="nowrap" flexGrow={1} height={1} overflow="hidden" columnGap={1}>
        {whole ? null : nav(`ar-prev-${name}`, '◀', cur > 0, cur - 1)}
        <Box key={`ar-marquee-${name}`} flexDirection="row" flexWrap="nowrap" flexGrow={1} height={1} overflow="hidden">
          {shown.map((c, i) => {
            const w = cellW?.[i] ?? natural(c)
            return [
              i > 0 ? <Text key={`ar-sep-${name}-${i}`} dimColor>{SEP}</Text> : null,
              <Box key={`ar-cell-${c.name}`} width={w} height={1} flexShrink={0} overflow="hidden">{cellBody(el, c, w, row.now)}</Box>,
            ]
          })}
        </Box>
        {whole ? null : <Text key={`ar-page-${name}`} dimColor>{`${cur + 1}/${pages.length}`}</Text>}
        {whole ? null : nav(`ar-next-${name}`, '▶', cur < pages.length - 1, cur + 1)}
      </Box>
    </Box>
  )
}

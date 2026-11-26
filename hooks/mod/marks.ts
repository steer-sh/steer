// Steer's colours and pixel marks. A mark is a pixel map drawn as terminal half-block
// cells: two pixels to a cell, the glyph's foreground and background.

/** Hue names the fix type; held and pass name the result. */
export const COLORS = {
  review: '#9badff',
  guard: '#e6c98a',
  check: '#8fd0c4',
  held: '#f0b49b',
  pass: '#c4d99b',
  ink: '#f5f7ed',
  dim: '#aeb8a7',
  ground: '#1b201a',
} as const

/** Below this many band columns the mark is left out. */
export const MARK_MIN_COLUMNS = 80

/** Milliseconds between two frames of a moving mark. */
export const FRAME_MS = 110

type Rgb = readonly [number, number, number]
/** A pixel: a colour and how much of it shows over the ground; null is empty. */
type Pixel = readonly [Rgb, number] | null

const hex = (h: string): Rgb => [parseInt(h.slice(1, 3), 16), parseInt(h.slice(3, 5), 16), parseInt(h.slice(5, 7), 16)]

const REVIEW = hex(COLORS.review)
const LIGHT = hex(COLORS.ink)
const GROUND = hex(COLORS.ground)

// The terminal's own colour, for a cell half that holds no pixel.
const DEFAULT = 0x01000000
const UPPER_HALF = 0x2580
const LOWER_HALF = 0x2584
const SPACE = 0x20

function pack(pixel: readonly [Rgb, number]): number {
  const [c, a] = pixel
  const mix = (i: 0 | 1 | 2) => Math.round(GROUND[i] + (c[i] - GROUND[i]) * a)
  return ((mix(0) << 16) | (mix(1) << 8) | mix(2)) >>> 0
}

/** Raster cells for a pixel map `columns` wide and `rows * 2` pixels tall. */
function cellsOf(columns: number, rows: number, at: (x: number, y: number) => Pixel): string {
  const words = new Uint32Array(columns * rows * 3)
  for (let row = 0; row < rows; row++)
    for (let x = 0; x < columns; x++) {
      const top = at(x, row * 2)
      const bottom = at(x, row * 2 + 1)
      const i = (row * columns + x) * 3
      if (top) {
        words[i] = UPPER_HALF
        words[i + 1] = pack(top)
        words[i + 2] = bottom ? pack(bottom) : DEFAULT
      } else if (bottom) {
        words[i] = LOWER_HALF
        words[i + 1] = pack(bottom)
        words[i + 2] = DEFAULT
      } else {
        words[i] = SPACE
        words[i + 1] = DEFAULT
        words[i + 2] = DEFAULT
      }
    }
  return (new Uint8Array(words.buffer) as Uint8Array & { toBase64(): string }).toBase64()
}

// The review mark: an open book. '#' is the outline, 't' a line of text.
const BOOK = [
  '#####...#####',
  '#....#.#....#',
  '#.ttt.#.ttt.#',
  '#.....#.....#',
  '#.ttt.#.ttt.#',
  '#.....#.....#',
  '#.ttt.#.ttt.#',
  '#############',
] as const

export const BOOK_COLUMNS = 13
export const BOOK_ROWS = 4

// The pixels the reading light visits: three lines down the left page, then the right.
const READ: (readonly [number, number])[] = []
for (const x0 of [1, 7]) for (const y of [2, 4, 6]) for (let i = 1; i < 4; i++) READ.push([x0 + i, y])

/** Which reading-light position frame `t` shows. */
export const bookStep = (t: number) => Math.floor(t / 2) % READ.length

/** The book. With `t` the reading light stands at that frame's place; without, the book is still. */
export function bookCells(t?: number): string {
  const k = t === undefined ? -1 : bookStep(t)
  const lit = k >= 0 ? READ[k] : undefined
  const prev = k > 0 ? READ[k - 1] : undefined
  const trail = lit && prev && prev[1] === lit[1] ? prev : undefined
  return cellsOf(BOOK_COLUMNS, BOOK_ROWS, (x, y) => {
    if (lit && lit[0] === x && lit[1] === y) return [LIGHT, 1]
    if (trail && trail[0] === x && trail[1] === y) return [REVIEW, 0.75]
    const ch = BOOK[y]?.[x]
    if (ch === '#') return [REVIEW, x === 6 ? 0.5 : 1]
    if (ch === 't') return [REVIEW, 0.32]
    return null
  })
}

export const RUN_COLUMNS = 8

/** One run's bar: a pulse crossing it at frame `t` while the run reads, solid once it returned. */
export function runCells(done: boolean, t: number, phase: number): string {
  const head = (t + phase) % (RUN_COLUMNS + 3)
  return cellsOf(RUN_COLUMNS, 1, (x, y) => {
    if (y !== 1) return null
    if (done) return [REVIEW, 1]
    const d = head - x
    return [REVIEW, d === 0 ? 1 : d === 1 ? 0.6 : d === 2 ? 0.3 : 0.14]
  })
}

const GUARD = hex(COLORS.guard)
const CHECK = hex(COLORS.check)
const HELD = hex(COLORS.held)
const PASS = hex(COLORS.pass)

/** Rows a block row's mark takes: three lines of text, six pixels. */
export const BLOCK_ROWS = 3

// The guard mark: a solid shield with a cross cut in. '#' is the shield, 'c' the cut.
const SHIELD = ['#######', '###c###', '#ccccc#', '###c###', '.##c##.', '..###..'] as const

export const SHIELD_COLUMNS = 7

/** Frames in one pass of the shine; a block shows salmon for the first BLOCK_FRAMES of it. */
export const SHINE_FRAMES = 36
const BLOCK_FRAMES = 9

const filled = (map: readonly string[], x: number, y: number) => (map[y]?.[x] ?? '.') !== '.'

/** A shield pixel with an empty neighbour or the map's edge beside it. */
const isEdge = (x: number, y: number) =>
  !filled(SHIELD, x - 1, y) || !filled(SHIELD, x + 1, y) || !filled(SHIELD, x, y - 1) || !filled(SHIELD, x, y + 1)

/**
 * The shield. With `t` a shine crosses it on the diagonal, and at a block the edge
 * and the cross are salmon for the first frames of the pass; without, it is still.
 */
export function shieldCells(t?: number): string {
  const k = t === undefined ? -1 : t % SHINE_FRAMES
  const blocking = k >= 0 && k < BLOCK_FRAMES
  return cellsOf(SHIELD_COLUMNS, BLOCK_ROWS, (x, y) => {
    const ch = SHIELD[y]?.[x]
    if (!ch || ch === '.') return null
    if (k >= 0 && x + y === k) return [LIGHT, 0.9]
    if (ch === 'c') return blocking ? [HELD, 1] : [GUARD, 0.3]
    return blocking && isEdge(x, y) ? [HELD, 1] : [GUARD, 1]
  })
}

// The check mark: a box holding a cross on a failure ('x') or a tick on recovery ('v').
const BOX_CROSS = ['########', '#xx..xx#', '#..xx..#', '#..xx..#', '#xx..xx#', '########'] as const
const BOX_TICK = ['########', '#....vv#', '#...vv.#', '#v.vv..#', '#.vv...#', '########'] as const

export const BOX_COLUMNS = 8

/** Frames between two blinks of the cross. */
const BLINK_FRAMES = 5

/** The box with a cross. With `t` the cross blinks; without, it shows. */
export function crossCells(t?: number): string {
  const shown = t === undefined || Math.floor(t / BLINK_FRAMES) % 2 === 0
  return cellsOf(BOX_COLUMNS, BLOCK_ROWS, (x, y) => {
    const ch = BOX_CROSS[y]?.[x]
    if (ch === '#') return [CHECK, 1]
    if (ch === 'x') return shown ? [HELD, 1] : [HELD, 0.25]
    return null
  })
}

/** Frames the tick takes to draw: one column a frame. */
export const TICK_FRAMES = BOX_COLUMNS

/** The box with a tick. With `t` the tick is drawn left to right up to that frame; without, it is whole. */
export function tickCells(t?: number): string {
  return cellsOf(BOX_COLUMNS, BLOCK_ROWS, (x, y) => {
    const ch = BOX_TICK[y]?.[x]
    if (ch === '#') return [CHECK, 1]
    if (ch === 'v') return t === undefined || x <= t ? [PASS, 1] : null
    return null
  })
}

// Steer's vector marks, for a surface that draws `Svg`: the band's ground, its rail,
// the book, a run's pulse, the guard's shield and the check's box. Each drawing is an SVG document that carries its light
// and dark shades. Desktop restarts an image's animations at each redraw, so each
// drawing places its animations `t` seconds in, and a redraw continues the motion.

/**
 * What a hue says: a review running, a commit or turn held, a commit or check let
 * through, a guard or a check, or a state at rest.
 */
export type Tone = 'review' | 'held' | 'pass' | 'guard' | 'check' | 'dim'

/** How the ground moves: `live` while a review runs, `once` for a block's entrance, `still` otherwise. */
export type Motion = 'live' | 'once' | 'still'

/**
 * What the book shows: `reading` moves a light down its lines, `marked` hangs a ribbon
 * in it, `open` is the book alone, `checked` carries a check on its right page and
 * `closed` is a book nobody opened.
 */
export type Pose = 'reading' | 'marked' | 'open' | 'checked' | 'closed'

// The motion of the ground's sky layer. Times are in seconds.
/** The strip that crosses again and again while a review runs: one crossing in `SWEEP_CROSS`, one start every `SWEEP_EVERY`. */
const SWEEP_CROSS = 4.6
const SWEEP_EVERY = 11
/** When the first repeating strip starts, after the state starts. */
const SWEEP_FIRST = 0.1
/** How strong the strip is at its centre, 0 to 1, on a light app and on a dark app. */
const SWEEP_LIGHT = 0.8
const SWEEP_DARK = 0.14

// The motion of the guard and check marks, each played once from when the state first drew. Times are in seconds.
/** How long the shield's edge and cross hold salmon at a block, then how long they take to fade back. */
const FLASH_HOLD = 0.9
const FLASH_FADE = 0.6
/** When the shine starts across the shield, and how long it takes to cross. */
const SHINE_AT = 0.5
const SHINE_CROSS = 1.1
/** How strong the shine is, 0 to 1, on a light app and on a dark app. */
const SHINE_LIGHT = 0.7
const SHINE_DARK = 0.55
/** How strong the cross cut into the shield shows, and how much darker it is than the shield. */
const CUT_OPACITY = 0.3
const CUT_DARK = 0.55
/** How much darker the shield's right half is. */
const SHADE_RIGHT = 0.22
/** How much the shield darkens toward its point, and lightens at its top. */
const SHADE_LOW = 0.16
const SHADE_TOP = 0.22
/** How strong the highlight on the shield's left rim is. */
const RIM_OPACITY = 0.35
/** How many times the check's cross blinks, and how long one blink takes. */
const BLINKS = 3
const BLINK_SECONDS = 0.6
/** How dim the cross is at the low point of a blink. */
const BLINK_LOW = 0.15
/** When the tick starts, and how long it takes to draw. */
const TICK_AT = 0.25
const TICK_DRAW = 0.6

// The tone on a light ground, on a dark ground, and a shade that reads on either.
const ON_LIGHT: Record<Tone, string> = {
  review: '#3157e8',
  held: '#c9562b',
  pass: '#4c7d1a',
  guard: '#9a6a10',
  check: '#1d7d6f',
  dim: '#868d82',
}
const ON_DARK: Record<Tone, string> = {
  review: '#9badff',
  held: '#f0b49b',
  pass: '#c4d99b',
  guard: '#e6c98a',
  check: '#8fd0c4',
  dim: '#aeb8a7',
}
const ON_EITHER: Record<Tone, string> = {
  review: '#5f7cf2',
  held: '#d9774f',
  pass: '#7da53c',
  guard: '#a8792a',
  check: '#2f9a8a',
  dim: '#9aa395',
}

/** A tone's colour for text, which reads on a light app and a dark one. */
export const textColor = (tone: Tone) => ON_EITHER[tone]

/** The `REVIEW` label's colour. */
export const LABEL_COLOR = textColor('review')

/** The card's ground and edge: a tint of the tone that reads on a light app and a dark one. */
export function cardColors(tone: Tone): { ground: string; edge: string } {
  return { ground: ON_EITHER[tone] + '17', edge: ON_EITHER[tone] + '55' }
}

/** A class, the property it colours and the tone it takes. */
type Shade = readonly [cls: string, prop: 'stroke' | 'fill' | 'stop-color', tone: Tone]

/**
 * CSS that gives each class its property in its tone's light shade, and the dark
 * shade under `prefers-color-scheme`. `light` and `dark` are each half's own rules.
 */
function shaded(shades: readonly Shade[], light = '', dark = ''): string {
  const half = (of: Record<Tone, string>) => shades.map(([cls, prop, tone]) => `.${cls}{${prop}:${of[tone]}}`).join('')
  return `${half(ON_LIGHT)}${light}@media (prefers-color-scheme:dark){${half(ON_DARK)}${dark}}`
}

/**
 * The CSS every ground and book drawing starts with. Shapes take these classes for
 * their colours: `ts` stroke and `tf` fill in the tone, `tc` a gradient stop in the
 * tone, `pg` a book page, `lm` the reading light and `sn` a stop of the crossing light.
 */
function paint(tone: Tone): string {
  return shaded(
    [
      ['ts', 'stroke', tone],
      ['tf', 'fill', tone],
      ['tc', 'stop-color', tone],
    ],
    `.pg{fill:#ffffff;fill-opacity:.92}.lm{stroke:${ON_LIGHT[tone]}}.sn{stop-color:#ffffff}`,
    `.pg{fill:${ON_DARK[tone]};fill-opacity:.14}.lm{stroke:#f5f7ed}.sn{stop-color:${ON_DARK[tone]}}`,
  )
}

const SVG = 'xmlns="http://www.w3.org/2000/svg"'
const n = (v: number) => String(Math.round(v * 100) / 100)

/** A time of `at` seconds, for CSS. */
const sec = (at: number) => `${n(at)}s`

/** An `animation-delay` that puts an animation due at `at` seconds where it is at `t`. */
const due = (at: number, t: number) => sec(at - t)

/** An SVG document of a size, in its own units, with its styles. */
const doc = (width: number, height: number, view: string, css: string) =>
  `<svg ${SVG} width="${width}" height="${height}" viewBox="${view}"><style>${css}</style>`

export const RAIL_WIDTH = 4

/** The rail down the card's left edge, `height` px tall. */
export function railSvg(tone: Tone, height: number): string {
  const h = n(Math.max(1, height))
  return (
    doc(RAIL_WIDTH, Math.max(1, height), `0 0 ${RAIL_WIDTH} ${h}`, paint(tone)) +
    `<defs><linearGradient id="r" x1="0" y1="0" x2="0" y2="1"><stop class="tc" offset="0"/><stop class="tc" offset="1" stop-opacity=".6"/></linearGradient></defs>` +
    `<rect width="${RAIL_WIDTH}" height="${h}" rx="2" fill="url(#r)"/></svg>`
  )
}

export const GROUND_HEIGHT = 170
export const WASH_WIDTH = 440
export const SKY_WIDTH = 480

/** The ground's left layer: a wash of the tone from the left edge and an entrance flash. `live` breathes it. */
export function washSvg(tone: Tone, motion: Motion, t: number): string {
  const css =
    paint(tone) +
    `.ai{animation:ai .9s ease-out ${due(0, t)} both}@keyframes ai{from{opacity:1}to{opacity:0}}` +
    (motion === 'live' ? `.ab{animation:ab 7s ease-in-out ${due(0, t)} infinite}@keyframes ab{0%,100%{opacity:.55}50%{opacity:1}}` : '')
  return (
    doc(WASH_WIDTH, GROUND_HEIGHT, `0 0 ${WASH_WIDTH} ${GROUND_HEIGHT}`, css) +
    `<defs><linearGradient id="w" x1="0" x2="1"><stop class="tc" offset="0" stop-opacity=".26"/><stop class="tc" offset=".4" stop-opacity=".1"/><stop class="tc" offset="1" stop-opacity="0"/></linearGradient></defs>` +
    `<rect class="ab" width="${WASH_WIDTH}" height="${GROUND_HEIGHT}" fill="url(#w)"/>` +
    `<rect class="ai" width="${WASH_WIDTH}" height="${GROUND_HEIGHT}" fill="url(#w)"/></svg>`
  )
}

/**
 * The ground's right layer: a glow of the tone toward the right edge. `live` adds a
 * light that crosses again and again, `once` one that crosses one time.
 */
export function skySvg(tone: Tone, motion: Motion, t: number): string {
  const live = motion !== 'still'
  // The part of a cycle, in percent, that the crossing takes; `once` plays one cycle.
  const cross = (SWEEP_CROSS / SWEEP_EVERY) * 100
  const repeat = motion === 'live' ? 'infinite' : '1 both'
  const css =
    paint(tone) +
    `.so{opacity:${SWEEP_LIGHT}}@media (prefers-color-scheme:dark){.so{opacity:${SWEEP_DARK}}}` +
    (live
      ? `.ah{opacity:0;animation:ah ${sec(SWEEP_EVERY)} linear ${due(SWEEP_FIRST, t)} ${repeat}}@keyframes ah{0%{transform:translateX(-260px);opacity:0}${n(cross * 0.12)}%{opacity:1}${n(cross * 0.86)}%{opacity:1}${n(cross)}%,100%{transform:translateX(${SKY_WIDTH}px);opacity:0}}`
      : '')
  // The mask fades the crossing light in from the layer's left edge.
  const light = `<g class="so" mask="url(#m)"><path class="ah" d="M70 0H260L190 ${GROUND_HEIGHT}H0Z" fill="url(#s)"/></g>`
  return (
    doc(SKY_WIDTH, GROUND_HEIGHT, `0 0 ${SKY_WIDTH} ${GROUND_HEIGHT}`, css) +
    `<defs><linearGradient id="e" x1="0" x2="1"><stop class="tc" offset="0" stop-opacity="0"/><stop class="tc" offset="1" stop-opacity=".16"/></linearGradient>` +
    `<linearGradient id="s" x1="0" x2="1"><stop class="sn" offset="0" stop-opacity="0"/><stop class="sn" offset=".5"/><stop class="sn" offset="1" stop-opacity="0"/></linearGradient>` +
    `<linearGradient id="f" x1="0" x2="1"><stop offset="0" stop-color="#ffffff" stop-opacity="0"/><stop offset=".3" stop-color="#ffffff"/></linearGradient>` +
    `<mask id="m"><rect width="${SKY_WIDTH}" height="${GROUND_HEIGHT}" fill="url(#f)"/></mask></defs>` +
    `<rect width="${SKY_WIDTH}" height="${GROUND_HEIGHT}" fill="url(#e)"/>` +
    (live ? light : '') +
    `</svg>`
  )
}

export const BOOK_WIDTH = 96
export const BOOK_HEIGHT = 60
/** The book's size beside a band of one line. */
export const BOOK_SMALL_WIDTH = 29
export const BOOK_SMALL_HEIGHT = 18

const LEFT_PAGE = 'M48 15C39 9 22 8.5 10 12.5V46C22 42 39 42.5 48 48.5Z'
const RIGHT_PAGE = 'M48 15C57 9 74 8.5 86 12.5V46C74 42 57 42.5 48 48.5Z'
const COVER = 'M5.5 17V51C21 47 38 48 48 54C58 48 75 47 90.5 51V17'
const CLOSED = 'M31 6H65A4 4 0 0 1 69 10V50A4 4 0 0 1 65 54H31A4 4 0 0 1 27 50V10A4 4 0 0 1 31 6Z'
const LINE_YS = [20, 27, 34, 41] as const
// A line of text on each page, drawn in reading direction.
const leftLine = (y: number) => `M17 ${n(y)}Q29 ${n(y - 3.6)} 42 ${n(y + 2.2)}`
const rightLine = (y: number) => `M54 ${n(y + 2.2)}Q67 ${n(y - 3.6)} 79 ${n(y)}`
const LEFT_LINES = LINE_YS.map(leftLine)
const LINES = [...LEFT_LINES, ...LINE_YS.map(rightLine)]
const LINE_SECONDS = 0.7
const READ_SECONDS = LINES.length * LINE_SECONDS + 0.6

/** The book in a tone and a pose, `small` for a band of one line. */
export function bookSvg(tone: Tone, pose: Pose, t: number, small = false): string {
  const stroke = small ? 5 : 2.4
  const reading = pose === 'reading'
  const checked = pose === 'checked'
  const lit = (LINE_SECONDS / READ_SECONDS) * 100
  const css =
    paint(tone) +
    (reading
      ? `.ag{animation:ag 5s ease-in-out ${due(0, t)} infinite}@keyframes ag{0%,100%{opacity:.45}50%{opacity:1}}` +
        `.al{opacity:0;animation:al ${n(READ_SECONDS)}s linear infinite}@keyframes al{0%{opacity:1;stroke-dashoffset:7}${n(lit)}%{opacity:1;stroke-dashoffset:-27}${n(lit + 0.01)}%,100%{opacity:0;stroke-dashoffset:-27}}`
      : checked
        ? `.ac{stroke-dasharray:34;animation:ac .5s ease-out ${due(0.25, t)} both}@keyframes ac{from{stroke-dashoffset:34}to{stroke-dashoffset:0}}`
        : '')
  const open = doc(small ? BOOK_SMALL_WIDTH : BOOK_WIDTH, small ? BOOK_SMALL_HEIGHT : BOOK_HEIGHT, `0 0 ${BOOK_WIDTH} ${BOOK_HEIGHT}`, css)
  const pen = `fill="none" stroke-linecap="round" stroke-linejoin="round"`

  if (pose === 'closed') {
    return (
      `${open}<path class="pg ts" d="${CLOSED}" stroke-width="${stroke}" stroke-linejoin="round"/>` +
      `<path class="ts" d="M36 6V54" ${pen} stroke-width="${stroke}"/>` +
      (small ? '' : `<path class="ts" d="M44 18H60M44 26H54" ${pen} stroke-width="2.2" stroke-opacity=".4"/>`) +
      `</svg>`
    )
  }

  const lines = small ? [] : checked ? LEFT_LINES : LINES
  const glow = reading
    ? `<defs><radialGradient id="h"><stop class="tc" offset="0" stop-opacity=".32"/><stop class="tc" offset="1" stop-opacity="0"/></radialGradient></defs><ellipse class="ag" cx="48" cy="30" rx="48" ry="30" fill="url(#h)"/>`
    : ''
  const light = reading
    ? LINES.map((d, i) => {
        const at = `style="animation-delay:${due(i * LINE_SECONDS, t)}"`
        return (
          `<path class="ts al" ${at} d="${d}" ${pen} stroke-opacity=".3" stroke-width="7.5" stroke-dasharray="7 80"/>` +
          `<path class="lm al" ${at} d="${d}" ${pen} stroke-width="3" stroke-dasharray="7 80"/>`
        )
      }).join('')
    : ''
  return (
    `${open}${glow}` +
    `<path class="ts" d="${COVER}" ${pen} stroke-width="${small ? 4 : 2}" stroke-opacity=".5"/>` +
    `<path class="pg ts" d="${LEFT_PAGE}" stroke-width="${stroke}" stroke-linejoin="round"/>` +
    `<path class="pg ts" d="${RIGHT_PAGE}" stroke-width="${stroke}" stroke-linejoin="round"/>` +
    lines.map(d => `<path class="ts" d="${d}" ${pen} stroke-width="2.2" stroke-opacity=".34"/>`).join('') +
    light +
    (pose === 'marked' ? `<path class="tf ts" d="M56 6H66V35L61 30.5L56 35Z" stroke-width="1.6" stroke-linejoin="round"/>` : '') +
    (checked ? `<path class="ts ac" d="M58 28L65.5 35L79 19" ${pen} stroke-width="${small ? 7 : 4.2}"/>` : '') +
    `</svg>`
  )
}

export const PULSE_WIDTH = 26
export const PULSE_HEIGHT = 8

/** One run's bar: a pulse crossing it while the run reads, solid once it returned. */
export function pulseSvg(done: boolean, t: number, offset: number): string {
  const track = (opacity: string) => `<rect class="tf" y="2.5" width="${PULSE_WIDTH}" height="3" rx="1.5" fill-opacity="${opacity}"/>`
  const view = `0 0 ${PULSE_WIDTH} ${PULSE_HEIGHT}`
  if (done) return `${doc(PULSE_WIDTH, PULSE_HEIGHT, view, paint('review'))}${track('1')}</svg>`
  return (
    doc(
      PULSE_WIDTH,
      PULSE_HEIGHT,
      view,
      paint('review') + `.ap{animation:ap 1.5s linear ${due(offset, t)} infinite}@keyframes ap{from{transform:translateX(-12px)}to{transform:translateX(${PULSE_WIDTH}px)}}`,
    ) +
    `<defs><linearGradient id="c" x1="0" x2="1"><stop class="tc" offset="0" stop-opacity="0"/><stop class="tc" offset="1"/></linearGradient><clipPath id="k"><rect y="2.5" width="${PULSE_WIDTH}" height="3" rx="1.5"/></clipPath></defs>` +
    `${track('.22')}<g clip-path="url(#k)"><rect class="ap" y="2.5" width="12" height="3" rx="1.5" fill="url(#c)"/></g></svg>`
  )
}

/**
 * A guard or check mark: `shield` for a guard, `cross` for a check that failed,
 * `tick` for a check that passes again, `unfixed` for one that ended unfixed and
 * `box`, the empty box, for a check at rest.
 */
export type Mark = 'shield' | 'cross' | 'tick' | 'unfixed' | 'box'

/** The mark's size beside three lines of text, and beside a band of one line. */
export const MARK_WIDTH = 64
export const MARK_HEIGHT = 60
export const MARK_SMALL_WIDTH = 19
export const MARK_SMALL_HEIGHT = 18

const SHIELD = 'M32 4L54 11V28C54 42.5 44.5 51.5 32 57C19.5 51.5 10 42.5 10 28V11Z'
const CUT = 'M28.5 14H35.5V23.5H45V30.5H35.5V46H28.5V30.5H19V23.5H28.5Z'
const BOX = 'M14 9H50A6 6 0 0 1 56 15V45A6 6 0 0 1 50 51H14A6 6 0 0 1 8 45V15A6 6 0 0 1 14 9Z'
const CROSS = 'M22.5 19.5L41.5 40.5M41.5 19.5L22.5 40.5'
const TICK = 'M20 31L28.5 39.5L44 21'
// The tick's length, for the stroke that draws it.
const TICK_LENGTH = 37

/**
 * The mark, in its hue and its result's. `moving` plays its motion once: the shield
 * shows salmon at its edge and cross, then a shine crosses it; the cross blinks; the
 * tick draws left to right. Without it, and for `unfixed`, the mark is still. `small`
 * is for a band of one line.
 */
export function markSvg(mark: Mark, moving: boolean, t: number, small = false): string {
  const hue: Tone = mark === 'shield' ? 'guard' : 'check'
  const moves = moving && mark !== 'unfixed' && mark !== 'box'
  const shades = shaded(
    [
      ['hf', 'fill', hue],
      ['hs', 'stroke', hue],
      ['rf', 'fill', 'held'],
      ['rs', 'stroke', 'held'],
      ['ps', 'stroke', 'pass'],
    ],
    `.sh{fill:#ffffff;opacity:${SHINE_LIGHT}}`,
    `.sh{fill:#f5f7ed;opacity:${SHINE_DARK}}`,
  )
  const hold = (FLASH_HOLD / (FLASH_HOLD + FLASH_FADE)) * 100
  const motion = !moves
    ? ''
    : mark === 'shield'
      ? `.af{opacity:0;animation:af ${sec(FLASH_HOLD + FLASH_FADE)} linear ${due(0, t)} both}@keyframes af{0%,${n(hold)}%{opacity:1}100%{opacity:0}}` +
        `.as{animation:as ${sec(SHINE_CROSS)} ease-in-out ${due(SHINE_AT, t)} both}@keyframes as{from{transform:translateX(0)}to{transform:translateX(80px)}}`
      : mark === 'cross'
        ? `.ab{animation:ab ${sec(BLINK_SECONDS)} linear ${due(0, t)} ${BLINKS} backwards}@keyframes ab{0%,50%{opacity:1}50.01%,100%{opacity:${BLINK_LOW}}}`
        : `.ak{stroke-dasharray:${TICK_LENGTH};animation:ak ${sec(TICK_DRAW)} ease-out ${due(TICK_AT, t)} both}@keyframes ak{from{stroke-dashoffset:${TICK_LENGTH}}to{stroke-dashoffset:0}}`
  const open = doc(small ? MARK_SMALL_WIDTH : MARK_WIDTH, small ? MARK_SMALL_HEIGHT : MARK_HEIGHT, `0 0 ${MARK_WIDTH} ${MARK_HEIGHT}`, shades + motion)
  const pen = `fill="none" stroke-linecap="round" stroke-linejoin="round"`

  if (mark === 'shield') {
    const flash = moves
      ? `<g class="af" clip-path="url(#k)"><path class="rs" d="${SHIELD}" fill="none" stroke-width="6"/><path class="rf" d="${CUT}" fill-opacity="${CUT_OPACITY}"/></g>` +
        `<g clip-path="url(#k)"><path class="sh as" d="M-6 -2H8L-12 62H-26Z"/></g>`
      : ''
    return (
      `${open}<defs><clipPath id="k"><path d="${SHIELD}"/></clipPath>` +
      `<mask id="u"><rect width="${MARK_WIDTH}" height="${MARK_HEIGHT}" fill="#ffffff"/><path d="${CUT}" fill="#000000"/></mask>` +
      `<linearGradient id="v" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffffff" stop-opacity="${SHADE_TOP}"/>` +
      `<stop offset=".45" stop-color="#ffffff" stop-opacity="0"/><stop offset="1" stop-color="#000000" stop-opacity="${SHADE_LOW}"/></linearGradient></defs>` +
      `<path class="hf" d="${SHIELD}" mask="url(#u)"/>` +
      `<g clip-path="url(#k)" mask="url(#u)"><rect x="32" width="32" height="${MARK_HEIGHT}" fill="#000000" fill-opacity="${SHADE_RIGHT}"/>` +
      `<rect width="${MARK_WIDTH}" height="${MARK_HEIGHT}" fill="url(#v)"/>` +
      `<path d="M32 4L10 11V28C10 42.5 19.5 51.5 32 57" fill="none" stroke="#ffffff" stroke-opacity="${RIM_OPACITY}" stroke-width="3"/></g>` +
      `<path class="hf" d="${CUT}"/><path d="${CUT}" fill="#000000" fill-opacity="${CUT_DARK}"/>` +
      `${flash}</svg>`
    )
  }

  const edge = small ? 7 : 4.5
  const box = `<path class="hf hs" d="${BOX}" fill-opacity=".12" stroke-width="${edge}" stroke-linejoin="round"/>`
  const inner = small ? 9 : 5.5
  const sign =
    mark === 'box'
      ? ''
      : mark === 'tick'
        ? `<path class="ps${moves ? ' ak' : ''}" d="${TICK}" ${pen} stroke-width="${inner}"/>`
        : `<path class="rs${moves ? ' ab' : ''}" d="${CROSS}" ${pen} stroke-width="${inner}"/>`
  return `${open}${box}${sign}</svg>`
}

/**
 * A clock for one drawing site: the seconds since the state `id` first drew there,
 * starting again when the site draws another state. The drawings place their
 * animations at this time, so a redraw continues them.
 */
export function stateClock(): (id: string, now: number) => number {
  let entered = { id: '', at: 0 }
  return (id, now) => {
    if (entered.id !== id) entered = { id, at: now }
    return Math.max(0, now - entered.at) / 1000
  }
}

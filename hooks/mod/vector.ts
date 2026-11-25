// Steer's vector marks, for a surface that draws `Svg`: the band's ground, its rail,
// the book and a run's pulse. Each drawing is an SVG document that carries its light
// and dark shades. Desktop restarts an image's animations at each redraw, so each
// drawing places its animations `t` seconds in, and a redraw continues the motion.

/** What the band's hue says: a review running, a commit held, a commit let through. */
export type Tone = 'review' | 'held' | 'pass'

/** How the ground moves: `live` while a review runs, `still` otherwise. */
export type Motion = 'live' | 'still'

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

// The tone on a light ground, on a dark ground, and a shade that reads on either.
const ON_LIGHT: Record<Tone, string> = { review: '#3157e8', held: '#c9562b', pass: '#4c7d1a' }
const ON_DARK: Record<Tone, string> = { review: '#9badff', held: '#f0b49b', pass: '#c4d99b' }
const ON_EITHER: Record<Tone, string> = { review: '#5f7cf2', held: '#d9774f', pass: '#7da53c' }

/** The `REVIEW` label's colour, which reads on a light app and a dark one. */
export const LABEL_COLOR = ON_EITHER.review

/** The card's ground and edge: a tint of the tone that reads on a light app and a dark one. */
export function cardColors(tone: Tone): { ground: string; edge: string } {
  return { ground: ON_EITHER[tone] + '17', edge: ON_EITHER[tone] + '55' }
}

/**
 * The CSS every drawing starts with. Shapes take these classes for their colours:
 * `ts` stroke and `tf` fill in the tone, `tc` a gradient stop in the tone, `pg` a book
 * page, `lm` the reading light and `sn` a stop of the crossing light.
 */
function paint(tone: Tone): string {
  const l = ON_LIGHT[tone]
  const d = ON_DARK[tone]
  return (
    `.ts{stroke:${l}}.tf{fill:${l}}.tc{stop-color:${l}}.pg{fill:#ffffff;fill-opacity:.92}.lm{stroke:${l}}.sn{stop-color:#ffffff}` +
    `@media (prefers-color-scheme:dark){.ts{stroke:${d}}.tf{fill:${d}}.tc{stop-color:${d}}.pg{fill:${d};fill-opacity:.14}.lm{stroke:#f5f7ed}.sn{stop-color:${d}}}`
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
 * light that crosses again and again.
 */
export function skySvg(tone: Tone, motion: Motion, t: number): string {
  const live = motion === 'live'
  // The part of a cycle, in percent, that the crossing takes.
  const cross = (SWEEP_CROSS / SWEEP_EVERY) * 100
  const css =
    paint(tone) +
    `.so{opacity:${SWEEP_LIGHT}}@media (prefers-color-scheme:dark){.so{opacity:${SWEEP_DARK}}}` +
    (live
      ? `.ah{opacity:0;animation:ah ${sec(SWEEP_EVERY)} linear ${due(SWEEP_FIRST, t)} infinite}@keyframes ah{0%{transform:translateX(-260px);opacity:0}${n(cross * 0.12)}%{opacity:1}${n(cross * 0.86)}%{opacity:1}${n(cross)}%,100%{transform:translateX(${SKY_WIDTH}px);opacity:0}}`
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

// The card a Steer band draws where the surface draws `Svg`: a tinted ground with a
// round edge, a wash and a sky behind it, a rail, a mark, then the band's lines and
// the status lines under them.

import type { BoxProps, ElementConstructor, RenderChildren, RenderElement, SvgProps, TextProps } from 'claude-code'

import { GROUND_HEIGHT, RAIL_WIDTH, SKY_WIDTH, WASH_WIDTH, cardColors, railSvg, skySvg, washSvg } from './vector'
import type { Motion, Tone } from './vector'

/** The height of a line of text where a band is drawn with `Svg`, in CSS px. */
export const ROW_PX = 20

/** An `Svg` drawing and its size. */
export type Drawing = { source: string; alt: string; width: number; height: number }

/** The rail's height beside `rows` lines of text. */
export const railHeight = (rows: number) => Math.max(1, rows * ROW_PX - 4)

/**
 * The card. `rows` is how many lines of text the rail runs beside; with status lines
 * the rail and the mark stay beside the band's own lines.
 */
export function vectorCard(
  els: { Box: ElementConstructor<BoxProps>; Svg: ElementConstructor<SvgProps> },
  card: { key: string; tone: Tone; motion: Motion; t: number; rows: number; mark?: Drawing; lines: RenderChildren[]; status: RenderChildren[] },
): RenderElement {
  const { Box, Svg } = els
  const { tone, motion, t, mark } = card
  const colors = cardColors(tone)
  const top = card.status.length > 0 ? ('flex-start' as const) : undefined
  const rail = railHeight(card.rows)
  return (
    <Box
      key={card.key}
      position="relative"
      flexDirection="row"
      alignItems="center"
      paddingX={1}
      overflow="hidden"
      backgroundColor={colors.ground}
      borderStyle="round"
      borderColor={colors.edge}
    >
      <Box key="steer-wash" position="absolute" top={-1} left={0} bottom={-1}>
        <Svg source={washSvg(tone, motion, t)} alt="steer" width={WASH_WIDTH} height={GROUND_HEIGHT} />
      </Box>
      <Box key="steer-sky" position="absolute" top={-1} right={0} bottom={-1}>
        <Svg source={skySvg(tone, motion, t)} alt="steer" width={SKY_WIDTH} height={GROUND_HEIGHT} />
      </Box>
      {/* Desktop paints a Box over the absolute art only when the Box holds an absolute child. */}
      <Box position="relative" marginRight={1} flexShrink={0} alignItems="center" alignSelf={top}>
        <Box position="absolute" top={0} left={0} />
        <Svg source={railSvg(tone, rail)} alt="steer" width={RAIL_WIDTH} height={rail} />
      </Box>
      {mark ? (
        <Box position="relative" marginRight={2} flexShrink={0} alignItems="center" alignSelf={top}>
          <Box position="absolute" top={0} left={0} />
          <Svg source={mark.source} alt={mark.alt} width={mark.width} height={mark.height} />
        </Box>
      ) : (
        ''
      )}
      <Box position="relative" flexDirection="column" flexGrow={1} flexShrink={1} minWidth={0}>
        <Box position="absolute" top={0} left={0} />
        {card.lines}
        {card.status}
      </Box>
    </Box>
  )
}

/**
 * One part of a line on a surface that draws `Svg`: plain text in a Box that gives way
 * by `shrink`, since Desktop truncates only a Text that holds plain text.
 */
export function textPart(
  els: { Box: ElementConstructor<BoxProps>; Text: ElementConstructor<TextProps> },
  text: string,
  props: TextProps = {},
  shrink = 1,
): RenderElement {
  const { Box, Text } = els
  return (
    <Box flexShrink={shrink} minWidth={0}>
      <Text {...props} wrap="truncate">
        {text}
      </Text>
    </Box>
  )
}

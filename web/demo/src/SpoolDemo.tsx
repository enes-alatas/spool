import { Fragment, type CSSProperties, type ReactNode } from 'react'
import {
  AbsoluteFill,
  Easing,
  interpolate,
  OffthreadVideo,
  Sequence,
  spring,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from 'remotion'
import { SpoolGlyph } from '../../src/components/Spool'
// Clip-relative seconds of each recorded moment, written by record.mjs.
import marks from '../public/marks.json'

// The control room's own tokens (web/src/styles.css), so the frame around
// the footage reads as Spool rather than as a template. The faces are the
// ones fonts.ts loads and record.mjs pins in the footage.
const T = {
  canvas: '#050505',
  surface: '#0c0c0e',
  hairline: '#24242a',
  text: '#f2f1ed',
  muted: '#7a7a7f',
  ready: '#3fe39a',
  mono: "'JetBrains Mono', monospace",
  sans: "'Inter', sans-serif",
}

export const W = 1920
export const H = 1080
export const FPS = 30
const s = (sec: number) => Math.round(sec * FPS)

// Beat boundaries, in seconds.
const B1 = [0, 5]
const B2 = [5, 22]
const B3 = [22, 31]
const B4 = [31, 39]
const B5 = [39, 47]
const B6 = [47, 52]
export const DURATION = s(52)

// The footage's CSS size; record.mjs captures it at 2x device pixels.
const PAGE = { w: 1280, h: 720 }

const clamp = { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' } as const
const fade = (frame: number, len: number, inF = 8, outF = 8) =>
  interpolate(frame, [0, inF, len - outF, len], [0, 1, 1, 0], clamp)

// Footage fitted to its box, then zoomed about a focus point (in page CSS
// pixels) that lands at the box's centre. zoom 1 shows the whole page. The
// camera is either one focus or keyframes in clip seconds, eased between, so
// it can follow the action as the clip plays.
type Focus = { x: number; y: number; zoom: number }
type Camera = Focus | [number, Focus][]
const ease = Easing.inOut(Easing.cubic)
function aim(camera: Camera, t: number): Focus {
  if (!Array.isArray(camera)) return camera
  const i = camera.findIndex(([at]) => at > t)
  if (i === 0) return camera[0][1]
  if (i === -1) return camera[camera.length - 1][1]
  const [t0, a] = camera[i - 1]
  const [t1, b] = camera[i]
  const k = ease((t - t0) / (t1 - t0))
  return { x: a.x + (b.x - a.x) * k, y: a.y + (b.y - a.y) * k, zoom: a.zoom + (b.zoom - a.zoom) * k }
}
function Footage({
  src,
  from = 0,
  rate = 1,
  camera,
  box,
}: {
  src: string
  from?: number
  rate?: number
  camera: Camera
  box: { w: number; h: number }
}) {
  const frame = useCurrentFrame()
  const f = aim(camera, from + (frame / FPS) * rate)
  const k = (box.w / PAGE.w) * f.zoom
  // Keep the page edge-to-edge: never pan past it.
  const tx = Math.min(0, Math.max(box.w - PAGE.w * k, box.w / 2 - f.x * k))
  const ty = Math.min(0, Math.max(box.h - PAGE.h * k, box.h / 2 - f.y * k))
  return (
    <div
      style={{ position: 'relative', overflow: 'hidden', background: T.canvas, width: box.w, height: box.h }}
    >
      <OffthreadVideo
        src={staticFile(src)}
        startFrom={s(from)}
        playbackRate={rate}
        muted
        style={{
          position: 'absolute',
          width: PAGE.w,
          height: PAGE.h,
          transformOrigin: '0 0',
          transform: `translate(${tx}px, ${ty}px) scale(${k})`,
        }}
      />
    </div>
  )
}

// A quiet browser frame: the footage sits inside it with room around it,
// instead of bleeding to the edges of the video.
const BAR = 40
function Window({
  width,
  url = 'localhost:8080',
  style,
  children,
}: {
  width: number
  url?: string
  style?: CSSProperties
  children: ReactNode
}) {
  const dot = (c: string) => <div style={{ width: 11, height: 11, borderRadius: 6, background: c }} />
  return (
    <div
      style={{
        width,
        borderRadius: 14,
        overflow: 'hidden',
        border: `1px solid ${T.hairline}`,
        background: T.surface,
        boxShadow: '0 40px 120px rgba(0,0,0,0.65), 0 0 0 1px rgba(255,255,255,0.02)',
        ...style,
      }}
    >
      <div
        style={{
          height: BAR,
          display: 'flex',
          alignItems: 'center',
          padding: '0 16px',
          gap: 8,
          borderBottom: `1px solid ${T.hairline}`,
          position: 'relative',
        }}
      >
        {dot('#2c2c31')}
        {dot('#2c2c31')}
        {dot('#2c2c31')}
        <div
          style={{
            position: 'absolute',
            left: '50%',
            transform: 'translateX(-50%)',
            fontFamily: T.mono,
            fontSize: 13,
            color: T.muted,
            background: T.canvas,
            border: `1px solid ${T.hairline}`,
            borderRadius: 7,
            padding: '4px 18px',
          }}
        >
          {url}
        </div>
      </div>
      {children}
    </div>
  )
}

// A window holding footage; the content keeps the page's 16:9 unless a
// height crops it to the part that matters.
function Screen({
  width,
  height,
  url,
  style,
  ...footage
}: { width: number; height?: number; url?: string; style?: CSSProperties } & Omit<
  Parameters<typeof Footage>[0],
  'box'
>) {
  return (
    <Window width={width} url={url} style={style}>
      <Footage
        {...footage}
        box={{ w: width - 2, h: height ?? Math.round(((width - 2) * PAGE.h) / PAGE.w) }}
      />
    </Window>
  )
}

// The backdrop every beat sits on: a soft light behind the window, so the
// frame reads as lit rather than as a black void around a screenshot.
function Stage({ children }: { children: ReactNode }) {
  return (
    <AbsoluteFill
      style={{
        background: [
          'radial-gradient(ellipse 58% 50% at 50% 44%, rgba(168, 178, 206, 0.16) 0%, rgba(168, 178, 206, 0.05) 45%, rgba(0, 0, 0, 0) 75%)',
          'radial-gradient(ellipse 90% 70% at 50% 0%, rgba(255, 255, 255, 0.05) 0%, rgba(0, 0, 0, 0) 60%)',
          '#08080a',
        ].join(', '),
      }}
    >
      {children}
    </AbsoluteFill>
  )
}

// The main window's geometry, shared by the full-width beats.
const MAIN = { width: 1440, top: 64 }
const MAIN_H = BAR + Math.round(((MAIN.width - 2) * PAGE.h) / PAGE.w) + 2
const CAPTION_TOP = MAIN.top + MAIN_H + 40

function Caption({
  step,
  children,
  delay = 10,
  left = (W - MAIN.width) / 2,
}: {
  step: string
  children: ReactNode
  delay?: number
  left?: number
}) {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const o = fade(frame - delay, durationInFrames - delay, 12, 10)
  const y = interpolate(frame - delay, [0, 16], [8, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  return (
    <div
      style={{
        position: 'absolute',
        top: CAPTION_TOP,
        left,
        right: left,
        display: 'flex',
        alignItems: 'baseline',
        gap: 28,
        opacity: o,
        transform: `translateY(${y}px)`,
      }}
    >
      <div
        style={{
          fontFamily: T.mono,
          fontSize: 15,
          fontWeight: 500,
          letterSpacing: 2.5,
          textTransform: 'uppercase',
          color: T.ready,
          whiteSpace: 'nowrap',
        }}
      >
        {step}
      </div>
      <div style={{ fontFamily: T.sans, fontSize: 34, fontWeight: 500, letterSpacing: -0.6, color: T.text }}>
        {children}
      </div>
    </div>
  )
}

// The control room's wordmark (App.tsx .wordmark), drawn at any size: the
// glyph, then "spool" in Nunito, lifted so the glyph's centre runs through the
// middle of the lowercase letters.
function Wordmark({ size = 23 }: { size?: number }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: size * 0.43,
        fontFamily: "'Nunito', sans-serif",
        fontSize: size,
        letterSpacing: '0.01em',
        color: T.text,
      }}
    >
      <div style={{ display: 'flex' }}>
        <SpoolGlyph size={size * 1.13} />
      </div>
      <span style={{ display: 'inline-block', transform: 'translateY(-0.065em)' }}>spool</span>
    </div>
  )
}

// Beat 1: the pitch, then the fleet.
function Pitch() {
  const frame = useCurrentFrame()
  const titleO = interpolate(frame, [0, 14, 58, 72], [0, 1, 1, 0], clamp)
  const titleY = interpolate(frame, [0, 20], [12, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  const fleetO = interpolate(frame, [62, 82], [0, 1], clamp)
  const rise = interpolate(frame, [62, 90], [24, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  return (
    <Stage>
      <AbsoluteFill style={{ opacity: fleetO }}>
        <Screen
          width={MAIN.width}
          style={{ position: 'absolute', top: MAIN.top + rise, left: (W - MAIN.width) / 2 }}
          src="fleet.mp4"
          from={marks.fleet.ready}
          camera={{ x: 640, y: 360, zoom: 1 }}
        />
        <Caption step="01 · Fleet" delay={86}>
          Every loop, its state and its spend, in one place.
        </Caption>
      </AbsoluteFill>
      <AbsoluteFill
        style={{
          opacity: titleO,
          justifyContent: 'center',
          padding: '0 240px',
          transform: `translateY(${titleY}px)`,
        }}
      >
        <Wordmark size={40} />
        <div
          style={{
            marginTop: 40,
            fontFamily: T.sans,
            color: T.text,
            fontSize: 68,
            fontWeight: 600,
            lineHeight: 1.12,
            letterSpacing: -2.4,
          }}
        >
          Your Claude Code loops, working together
          <br />
          <span style={{ color: T.muted }}>and keeping you in the conversation.</span>
        </div>
      </AbsoluteFill>
    </Stage>
  )
}

const mainScreen: CSSProperties = { position: 'absolute', top: MAIN.top, left: (W - MAIN.width) / 2 }

// Beat 2: create a loop, give it a bot. The form fill plays fast, the rest at
// about real pace, and the camera follows the pointer: the form, then the
// Surfaces panel once the loop's page opens (its fresh timeline is noise).
const C = marks.create
const FILL_RATE = 1.6
const REST_RATE = 1.1
const FILL_LEN = (C['scroll-create'] - C.ready) / FILL_RATE
const SCROLL_LEN = (C['loop-page'] - C['scroll-create']) / REST_RATE
const form: Focus = { x: 615, y: 330, zoom: 1.25 }
const side: Focus = { x: 1022, y: 380, zoom: 2 }
const camera: Camera = [
  [C['create-click'], form],
  [C['loop-page'] - 0.3, form],
  [C['loop-page'] + 0.6, side],
]
function Create() {
  return (
    <Stage>
      <Sequence durationInFrames={s(FILL_LEN)}>
        <Screen
          width={MAIN.width}
          url="localhost:8080/new"
          style={mainScreen}
          src="create.mp4"
          from={C.ready}
          rate={FILL_RATE}
          camera={[
            [C.ready, { x: 640, y: 360, zoom: 1 }],
            [C.ready + 1.2, form],
          ]}
        />
      </Sequence>
      <Sequence from={s(FILL_LEN)} durationInFrames={s(SCROLL_LEN)}>
        <Screen
          width={MAIN.width}
          url="localhost:8080/new"
          style={mainScreen}
          src="create.mp4"
          from={C['scroll-create']}
          rate={REST_RATE}
          camera={camera}
        />
      </Sequence>
      <Sequence from={s(FILL_LEN + SCROLL_LEN)}>
        <Screen
          width={MAIN.width}
          url="localhost:8080/loops/scout"
          style={mainScreen}
          src="create.mp4"
          from={C['loop-page']}
          rate={REST_RATE}
          camera={camera}
        />
      </Sequence>
      <Caption step="02 · Create">Start a loop, then give it a Telegram bot.</Caption>
    </Stage>
  )
}

// A chat pane, drawn rather than recorded: a Telegram-like thread, labelled.
type Line = { at: number; who: string; text: string; me?: boolean }
function Bubble({ at, who, text, me }: Line) {
  const frame = useCurrentFrame()
  const { fps } = useVideoConfig()
  const p = spring({ frame: frame - at, fps, config: { damping: 18, stiffness: 140 } })
  if (frame < at) return null
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: me ? 'flex-end' : 'flex-start',
        opacity: p,
        transform: `translateY(${(1 - p) * 14}px)`,
      }}
    >
      <div
        style={{
          maxWidth: '86%',
          background: me ? '#2b5278' : '#182533',
          color: '#f5f7fa',
          borderRadius: 16,
          padding: '11px 15px 12px',
          fontFamily: T.sans,
          fontSize: 19,
          lineHeight: 1.4,
          marginBottom: 12,
        }}
      >
        {!me && who && (
          <div style={{ color: '#6ab3f3', fontSize: 15, fontWeight: 600, marginBottom: 4 }}>{who}</div>
        )}
        {text}
      </div>
    </div>
  )
}

function Typing({ who, from, to }: { who: string; from: number; to: number }) {
  const frame = useCurrentFrame()
  if (frame < from || frame >= to) return null
  const dots = '•••'.slice(0, 1 + (Math.floor(frame / 6) % 3))
  return (
    <div style={{ color: '#6ab3f3', fontFamily: T.sans, fontSize: 16, margin: '2px 4px 12px' }}>
      {who} is typing {dots}
    </div>
  )
}

const CHAT = { w: 420, h: 792 }
function ChatPane({
  title,
  subtitle,
  avatar,
  tint,
  lines,
  typing,
  style,
}: {
  title: string
  subtitle: string
  avatar: string
  tint: string
  lines: Line[]
  typing?: { who: string; from: number; to: number; before: number }
  style?: CSSProperties
}) {
  return (
    <div
      style={{
        width: CHAT.w,
        height: CHAT.h,
        background: '#0e1621',
        borderRadius: 24,
        border: `1px solid ${T.hairline}`,
        overflow: 'hidden',
        display: 'flex',
        flexDirection: 'column',
        boxShadow: '0 40px 120px rgba(0,0,0,0.65)',
        ...style,
      }}
    >
      <div
        style={{
          background: '#17212b',
          padding: '16px 20px',
          display: 'flex',
          alignItems: 'center',
          gap: 14,
        }}
      >
        <div
          style={{
            width: 42,
            height: 42,
            borderRadius: 21,
            background: tint,
            color: '#fff',
            display: 'grid',
            placeItems: 'center',
            fontFamily: T.sans,
            fontWeight: 600,
            fontSize: 18,
          }}
        >
          {avatar}
        </div>
        <div>
          <div style={{ color: '#f5f7fa', fontFamily: T.sans, fontWeight: 600, fontSize: 18 }}>{title}</div>
          <div style={{ color: '#7f91a4', fontFamily: T.sans, fontSize: 14 }}>{subtitle}</div>
        </div>
      </div>
      <div
        style={{
          flex: 1,
          padding: '20px 16px',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'flex-end',
        }}
      >
        {lines.map((l, i) => (
          <Fragment key={i}>
            {typing?.before === i && <Typing {...typing} />}
            <Bubble {...l} />
          </Fragment>
        ))}
      </div>
    </div>
  )
}

// Beats 3 and 5 pair a window with a chat pane, side by side as one centred
// group.
const PAIR = { width: 1100, height: 750, gap: 56 }
const PAIR_LEFT = (W - PAIR.width - PAIR.gap - CHAT.w) / 2
const PAIR_H = BAR + PAIR.height + 2
function Pair({
  step,
  caption,
  chat,
  ...screen
}: { step: string; caption: ReactNode; chat: Parameters<typeof ChatPane>[0] } & Omit<
  Parameters<typeof Screen>[0],
  'width' | 'height' | 'style'
>) {
  const frame = useCurrentFrame()
  const slide = interpolate(frame, [0, 18], [40, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  const chatO = interpolate(frame, [0, 14], [0, 1], clamp)
  return (
    <Stage>
      <Screen
        width={PAIR.width}
        height={PAIR.height}
        style={{ position: 'absolute', left: PAIR_LEFT, top: MAIN.top + (MAIN_H - PAIR_H) / 2 }}
        {...screen}
      />
      <ChatPane
        {...chat}
        style={{
          position: 'absolute',
          left: PAIR_LEFT + PAIR.width + PAIR.gap,
          top: MAIN.top + (MAIN_H - CHAT.h) / 2,
          opacity: chatO,
          transform: `translateX(${slide}px)`,
        }}
      />
      <Caption step={step} left={PAIR_LEFT}>
        {caption}
      </Caption>
    </Stage>
  )
}

// Beat 3: a person asks in a Telegram group; the loop's own timeline shows
// the same message arriving and the answer going out. The window is cropped
// to the timeline column, leaving the side panel out.
function Talk() {
  return (
    <Pair
      step="03 · Talk"
      caption="Message it in a Telegram group. It answers as itself."
      url="localhost:8080/loops/gardener"
      src="talk.mp4"
      from={marks.talk.ready}
      camera={{ x: 445, y: 370, zoom: 1.5 }}
      chat={{
        title: 'Docs team',
        subtitle: 'Telegram group · rana, @gardener_bot',
        avatar: 'D',
        tint: '#3a6e5a',
        typing: { who: 'gardener', from: s(2.2), to: s(5.4), before: 1 },
        lines: [
          {
            at: s(0.6),
            who: 'rana',
            text: '@gardener the install page still tells people to run `make setup`, which we removed last week. Can you take a pass?',
          },
          {
            at: s(5.4),
            who: 'gardener',
            text: 'Three pages, all fixed — PR #48. The quickstart also showed the old output, so that block went too.',
          },
        ],
      }}
    />
  )
}

// Beat 4: the fleet channel, where loops and people meet.
function Coordinate() {
  return (
    <Stage>
      <Screen
        width={MAIN.width}
        url="localhost:8080/?view=channel"
        style={mainScreen}
        src="channel.mp4"
        from={marks.channel.ready}
        camera={[
          [marks.channel.ready, { x: 640, y: 360, zoom: 1 }],
          [marks.channel.ready + 8, { x: 660, y: 430, zoom: 1.15 }],
        ]}
      />
      <Caption step="04 · Coordinate">Loops and people share one channel. You follow along.</Caption>
    </Stage>
  )
}

// Beat 5: what the channel settled reaches its owner privately, with the one
// question that is theirs to answer. The channel stays in view beside it.
function Report() {
  return (
    <Pair
      step="05 · Report"
      caption="When a call is yours, the loop brings it to you."
      url="localhost:8080/?view=channel"
      src="channel.mp4"
      from={marks.channel.ready + 8}
      camera={{ x: 640, y: 360, zoom: 1 }}
      chat={{
        title: 'watcher',
        subtitle: 'Telegram · private chat · bot',
        avatar: 'W',
        tint: '#6b4f9e',
        typing: { who: 'watcher', from: s(0.4), to: s(1.6), before: 0 },
        lines: [
          {
            at: s(1.6),
            who: '',
            text: 'Nightly broke again at the same fixture. The operator is chasing the author of the open change, so I have not reverted it. If it is still red at 09:00 tomorrow, should I revert?',
          },
          { at: s(4.6), who: 'you', me: true, text: 'Yes, revert at 09:00 if it is still red.' },
        ],
      }}
    />
  )
}

// Beat 6: the end card, on a clean canvas.
function EndCard() {
  const frame = useCurrentFrame()
  const lines = ['Keep work going.', 'Stay involved.', 'Coordinate the fleet.']
  const up = (at: number) => ({
    opacity: interpolate(frame, [at, at + 14], [0, 1], clamp),
    transform: `translateY(${interpolate(frame, [at, at + 18], [10, 0], { ...clamp, easing: Easing.out(Easing.cubic) })}px)`,
  })
  return (
    <Stage>
      <AbsoluteFill style={{ justifyContent: 'center', padding: '0 240px' }}>
        <div style={up(4)}>
          <Wordmark size={40} />
        </div>
        <div style={{ marginTop: 40 }}>
          {lines.map((l, i) => (
            <div
              key={l}
              style={{
                ...up(12 + i * 12),
                fontFamily: T.sans,
                color: i === 2 ? T.text : T.muted,
                fontSize: 76,
                fontWeight: 600,
                letterSpacing: -2.4,
                lineHeight: 1.1,
              }}
            >
              {l}
            </div>
          ))}
        </div>
        <div style={{ ...up(64), marginTop: 56, fontFamily: T.mono, fontSize: 22, color: T.ready }}>
          github.com/enes-alatas/spool
        </div>
      </AbsoluteFill>
    </Stage>
  )
}

function Beat({ span, children }: { span: number[]; children: ReactNode }) {
  const len = s(span[1] - span[0])
  return (
    <Sequence from={s(span[0])} durationInFrames={len}>
      <FadeWrap len={len}>{children}</FadeWrap>
    </Sequence>
  )
}

function FadeWrap({ len, children }: { len: number; children: ReactNode }) {
  const frame = useCurrentFrame()
  return <AbsoluteFill style={{ opacity: fade(frame, len, 8, 8) }}>{children}</AbsoluteFill>
}

export const SpoolDemo = () => (
  <AbsoluteFill style={{ background: T.canvas }}>
    <Beat span={B1}>
      <Pitch />
    </Beat>
    <Beat span={B2}>
      <Create />
    </Beat>
    <Beat span={B3}>
      <Talk />
    </Beat>
    <Beat span={B4}>
      <Coordinate />
    </Beat>
    <Beat span={B5}>
      <Report />
    </Beat>
    <Beat span={B6}>
      <EndCard />
    </Beat>
  </AbsoluteFill>
)

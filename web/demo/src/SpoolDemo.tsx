import { Fragment, type CSSProperties, type ReactNode } from 'react'
import {
  AbsoluteFill,
  Easing,
  Img,
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

const { times } = marks

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
  page = PAGE,
}: {
  src: string
  from?: number
  rate?: number
  camera: Camera
  box: { w: number; h: number }
  // The clip's CSS size, when record.mjs took it at another viewport.
  page?: { w: number; h: number }
}) {
  const frame = useCurrentFrame()
  const f = aim(camera, from + (frame / FPS) * rate)
  const k = (box.w / page.w) * f.zoom
  // Keep the page edge-to-edge: never pan past it.
  const tx = Math.min(0, Math.max(box.w - page.w * k, box.w / 2 - f.x * k))
  const ty = Math.min(0, Math.max(box.h - page.h * k, box.h / 2 - f.y * k))
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
          width: page.w,
          height: page.h,
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

// Beat 1: the pitch.
function Pitch() {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const titleO = interpolate(frame, [0, 14, durationInFrames - 14, durationInFrames], [0, 1, 1, 0], clamp)
  const titleY = interpolate(frame, [0, 20], [12, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  return (
    <Stage>
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

// Beat 2: create a loop from the first-run Loops card (#733). The form
// fill plays fast, then the press at real pace; the wait for the first
// wake is cut, the wake's tick and the reach for Done play fast, and the
// beat ends a moment after Done, on the card's own tick. Its bot comes in
// the next beat, on the same page.
const C = marks.create
const FILL_RATE = 2
const FILL_LEN = (C['create-click'] + 0.3 - C.ready) / FILL_RATE
const PRESS_FROM = C['create-click'] + 0.3
const PRESS_LEN = C.created + 0.9 - PRESS_FROM
const WOKEN_FROM = C.woken - 0.5
const WOKEN_RATE = 1.5
const WOKEN_LEN = (C.done - WOKEN_FROM) / WOKEN_RATE
// The hold after Done: long enough for the card's tick to draw in.
const DONE_HOLD = 1
const CREATE_LEN = FILL_LEN + PRESS_LEN + WOKEN_LEN + DONE_HOLD
// The whole page: the dialog sits in its middle, and any zoom would cut
// the top bar to a sliver.
const firstRun: Focus = { x: PAGE.w / 2, y: PAGE.h / 2, zoom: 1 }
function Create() {
  const shot = {
    width: MAIN.width,
    url: 'localhost:8080/',
    style: mainScreen,
    src: 'create.mp4',
    camera: firstRun,
  }
  return (
    <Stage>
      <Sequence durationInFrames={s(FILL_LEN)}>
        <Screen {...shot} from={C.ready} rate={FILL_RATE} />
      </Sequence>
      <Sequence from={s(FILL_LEN)} durationInFrames={s(PRESS_LEN)}>
        <Screen {...shot} from={PRESS_FROM} />
      </Sequence>
      <Sequence from={s(FILL_LEN + PRESS_LEN)} durationInFrames={s(WOKEN_LEN)}>
        <Screen {...shot} from={WOKEN_FROM} rate={WOKEN_RATE} />
      </Sequence>
      <Sequence from={s(FILL_LEN + PRESS_LEN + WOKEN_LEN)}>
        <Screen {...shot} from={C.done} />
      </Sequence>
      <Caption step="01 · Create">Start a loop: a name and a mission.</Caption>
    </Stage>
  )
}

// A chat line: who sent it, and when it lands in clip frames. time is the
// clock the room stamps the same message with; image is a photo the text
// rides under as its caption, as Telegram draws one.
type Line = {
  at: number
  who: string
  text: string
  me?: boolean
  time?: string
  image?: string
  reactions?: { at: number; chips: { emoji: string; count: number }[] }
}
function Bubble({ at, who, text, me, time, image, reactions }: Line) {
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
          overflow: 'hidden',
          fontFamily: T.sans,
          fontSize: 19,
          lineHeight: 1.4,
          marginBottom: 12,
        }}
      >
        {!me && who && (
          <div style={{ color: '#6ab3f3', fontSize: 15, fontWeight: 600, padding: '11px 15px 0' }}>{who}</div>
        )}
        {image && (
          <Img
            src={staticFile(image)}
            style={{ display: 'block', width: '100%', marginTop: who && !me ? 8 : 0 }}
          />
        )}
        <div style={{ padding: `${who && !me && !image ? 4 : 11}px 15px 9px` }}>
          {text}
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: 10, marginTop: 4 }}>
            <div style={{ flex: 1 }}>{reactions && <Reactions {...reactions} />}</div>
            {time && (
              <div
                style={{ fontSize: 13, color: me ? 'rgba(220, 235, 255, 0.6)' : 'rgba(220, 230, 240, 0.45)' }}
              >
                {time}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

// The reactions under a bubble's text, popping in as Telegram draws them.
function Reactions({ at, chips }: { at: number; chips: { emoji: string; count: number }[] }) {
  const frame = useCurrentFrame()
  const { fps } = useVideoConfig()
  if (frame < at) return null
  return (
    <div style={{ display: 'flex', gap: 6, marginTop: 4 }}>
      {chips.map((c, i) => {
        const p = spring({ frame: frame - at - i * 4, fps, config: { damping: 11, stiffness: 180 } })
        return (
          <div
            key={c.emoji}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              padding: '3px 10px',
              borderRadius: 14,
              background: 'rgba(255, 255, 255, 0.16)',
              fontSize: 17,
              transform: `scale(${Math.max(p, 0)})`,
              transformOrigin: 'left center',
            }}
          >
            <span style={{ fontFamily: "'Noto Color Emoji'" }}>{c.emoji}</span>
            <span style={{ fontSize: 15 }}>{c.count}</span>
          </div>
        )
      })}
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

// A chat pane, drawn rather than recorded: a Telegram-like thread, labelled.
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

// Beats 3 and 5 to 7 pair a window with a chat pane, side by side as one
// centred group.
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

// Beat 3: the first-run Chat surface card gives scout its Telegram bot,
// and the operator links their own account through it (#733). The attach
// and the link play a little above real pace, in Pair's layout; the chat
// pane is the operator's DM with the bot, on record.mjs's cues, in the
// hub's own words.
const LK = marks.link
const ATTACH_RATE = 1.25
const LINK_RATE = 1.25
const ATTACH_LEN = (LK.attached - LK.ready) / ATTACH_RATE
const LINK_FROM = LK.attached - 0.2
// The beat's frame for a moment in the clip's second half.
const atLink = (t: number) => s(ATTACH_LEN + (t - LINK_FROM) / LINK_RATE)
const LINK_LEN = ATTACH_LEN + (LK.end - LINK_FROM) / LINK_RATE
// Zoomed enough that the page fills the window's height, which is taller
// than the clip's 16:9, centred on the dialog.
const linkCamera: Focus = { x: PAGE.w / 2, y: PAGE.h / 2, zoom: 1.22 }
function Link() {
  const frame = useCurrentFrame()
  const slide = interpolate(frame, [0, 18], [40, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  const chatO = interpolate(frame, [0, 14], [0, 1], clamp)
  const screen = {
    width: PAIR.width,
    height: PAIR.height,
    url: 'localhost:8080/',
    src: 'link.mp4',
    camera: linkCamera,
    style: { position: 'absolute', left: PAIR_LEFT, top: MAIN.top + (MAIN_H - PAIR_H) / 2 } as CSSProperties,
  }
  return (
    <Stage>
      <Sequence durationInFrames={s(ATTACH_LEN)}>
        <Screen {...screen} from={LK.ready} rate={ATTACH_RATE} />
      </Sequence>
      <Sequence from={s(ATTACH_LEN)}>
        <Screen {...screen} from={LINK_FROM} rate={LINK_RATE} />
      </Sequence>
      <ChatPane
        title="scout"
        subtitle="Telegram · private chat · bot"
        avatar="S"
        tint="#9e6b4f"
        lines={[
          { at: atLink(LK.sent), who: 'you', me: true, text: 'link-demo4821' },
          {
            at: atLink(LK.sent + 0.8),
            who: '',
            text: "Spool: this links your Telegram account to admin in the control room. admin will read your private conversations with this hub's loops, and you can message every one of them. Reply YES within 5 minutes to link, or ignore this to leave it unlinked.",
          },
          { at: atLink(LK.yes - 0.6), who: 'you', me: true, text: 'YES' },
          {
            at: atLink(LK.yes + 0.2),
            who: '',
            text: 'Spool: this account is now linked to admin in the control room, and can message every loop here.',
          },
          { at: atLink(LK.again), who: 'you', me: true, text: 'hi scout' },
        ]}
        style={{
          position: 'absolute',
          left: PAIR_LEFT + PAIR.width + PAIR.gap,
          top: MAIN.top + (MAIN_H - CHAT.h) / 2,
          opacity: chatO,
          transform: `translateX(${slide}px)`,
        }}
      />
      <Caption step="02 · Link" left={PAIR_LEFT}>
        Give it a Telegram or Slack bot, then link yourself. You become its owner.
      </Caption>
    </Stage>
  )
}

// Beat 4: the fleet, scout among it.
function Fleet() {
  const frame = useCurrentFrame()
  const rise = interpolate(frame, [0, 28], [24, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  return (
    <Stage>
      <Screen
        width={MAIN.width}
        style={{ ...mainScreen, top: MAIN.top + rise }}
        src="fleet.mp4"
        from={marks.fleet.ready}
        camera={{ x: PAGE.w / 2, y: PAGE.h / 2, zoom: 1 }}
      />
      <Caption step="03 · Fleet">Every loop, its state and its spend, in one place.</Caption>
    </Stage>
  )
}

// Beat 5: a person asks in the Telegram group that carries the fleet
// channel, and the loop answers there. The fleet channel, beside it, shows
// each message as it lands: record.mjs reveals them on the same cues.
const TK = marks.talk
// record.mjs takes the talk and report clips at the window's own size
// (PAIR's content box), so they play at 1:1 with nothing cropped.
const TALK_PAGE = { w: 1098, h: 750 }
function Talk() {
  return (
    <Pair
      step="04 · Talk"
      caption="Talk to loops in Telegram or Slack. The room shows it as it happens."
      url="localhost:8080/?view=channel"
      src="talk.mp4"
      from={TK.ready}
      page={TALK_PAGE}
      camera={{ x: TALK_PAGE.w / 2, y: TALK_PAGE.h / 2, zoom: 1 }}
      chat={{
        title: 'Handbook crew',
        subtitle: 'Telegram group · the fleet channel',
        avatar: 'T',
        tint: '#3a6e5a',
        typing: { who: 'gardener', from: s(TK.ask - TK.ready + 1), to: s(TK.answer - TK.ready), before: 1 },
        lines: [
          {
            at: s(TK.ask - TK.ready),
            who: 'rana',
            image: 'install-page.png',
            time: times.talk.ask,
            text: '@gardener the install page still tells people to run `make setup`, which we removed last week. Can you take a pass?',
          },
          {
            at: s(TK.answer - TK.ready),
            who: 'gardener',
            time: times.talk.answer,
            text: 'Three pages, all fixed — PR #48. The quickstart also showed the old output, so that block went too.',
            reactions: {
              at: s(TK.reactions - TK.ready),
              chips: [
                { emoji: '👍', count: 2 },
                { emoji: '🎉', count: 1 },
              ],
            },
          },
        ],
      }}
    />
  )
}

// Beat 6: a channel of its own for two loops and their owner, carried by a
// Telegram group of its own, beside its pane on one loop's page.
const DK = marks.docs
function Channels() {
  return (
    <Pair
      step="05 · Channels"
      caption="Give a few loops a channel of their own."
      url="localhost:8080/loops/gardener?channel=docs"
      src="docs.mp4"
      from={DK.ready}
      camera={{ x: 445, y: 380, zoom: 1.45 }}
      chat={{
        title: 'Docs reviewers',
        subtitle: 'Telegram group · 3 members',
        avatar: 'D',
        tint: '#4f6e9e',
        typing: {
          who: 'gardener',
          from: s(DK.line0 - DK.ready + 0.8),
          to: s(DK.line1 - DK.ready),
          before: 1,
        },
        lines: [
          {
            at: s(DK.line0 - DK.ready),
            who: 'archivist',
            time: times.docs.line0,
            text: '@gardener the incident write-up template links the old runbook. Yours or mine?',
          },
          {
            at: s(DK.line1 - DK.ready),
            who: 'gardener',
            time: times.docs.line1,
            text: 'Mine, it moved with the handbook. Fixed in the same PR as the install page.',
          },
          {
            at: s(DK.line2 - DK.ready),
            who: 'you',
            me: true,
            time: times.docs.line2,
            text: '@archivist once that lands, close the stale-runbook issue too.',
          },
        ],
      }}
    />
  )
}

// Beat 7: a call that is the owner's reaches them privately, and the loop
// acknowledges the answer. Beside the DM, watcher's own timeline: the room
// does not draw a DM's text, so it shows the loop's side of the exchange,
// each step on the cue record.mjs revealed it.
const RK = marks.report
function Report() {
  return (
    <Pair
      step="06 · Report"
      caption="When a call is yours, the loop brings it to you."
      url="localhost:8080/loops/watcher"
      src="report.mp4"
      from={RK.ready}
      page={TALK_PAGE}
      camera={{ x: TALK_PAGE.w / 2, y: TALK_PAGE.h / 2, zoom: 1 }}
      chat={{
        title: 'watcher',
        subtitle: 'Telegram · private chat · bot',
        avatar: 'W',
        tint: '#6b4f9e',
        typing: { who: 'watcher', from: s(0.3), to: s(RK.ask - RK.ready), before: 0 },
        lines: [
          {
            at: s(RK.ask - RK.ready),
            who: '',
            time: times.report.ask,
            text: 'Nightly broke again at the same fixture. The operator is chasing the author of the open change, so I have not reverted it. If it is still red at 09:00 tomorrow, should I revert?',
          },
          {
            at: s(RK.reply - RK.ready),
            who: 'you',
            me: true,
            time: times.report.reply,
            text: 'Yes, revert at 09:00 if it is still red.',
            reactions: { at: s(RK.react - RK.ready), chips: [{ emoji: '👍', count: 1 }] },
          },
        ],
      }}
    />
  )
}

// Beat 8: the end card, on a clean canvas.
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

// Beat boundaries, in seconds: each beat's length, laid end to end.
const LENGTHS = [3, CREATE_LEN, LINK_LEN, 5, 9, 8, 8, 5]
const SPANS = LENGTHS.map((len, i) => {
  const from = LENGTHS.slice(0, i).reduce((a, b) => a + b, 0)
  return [from, from + len]
})
export const DURATION = s(SPANS[SPANS.length - 1][1])

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

const BEATS = [Pitch, Create, Link, Fleet, Talk, Channels, Report, EndCard]

export const SpoolDemo = () => (
  <AbsoluteFill style={{ background: T.canvas }}>
    {BEATS.map((B, i) => (
      <Beat key={i} span={SPANS[i]}>
        <B />
      </Beat>
    ))}
  </AbsoluteFill>
)

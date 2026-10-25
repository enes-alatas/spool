import { AbsoluteFill, Easing, interpolate, Sequence, useCurrentFrame, useVideoConfig } from 'remotion'
import marks from '../public/marks.json'
import {
  Beat,
  Caption,
  clamp,
  MAIN,
  mainScreen,
  PAGE,
  Pair,
  s,
  Screen,
  Stage,
  T,
  TALK_PAGE,
  Wordmark,
  type Focus,
} from './SpoolDemo'

// The cloud loop video (#1126), beside the README's demo: the same frame,
// shorter. A title, New loop on the cloud runtime, the loop's page saying
// where it runs as its first answer lands, then the loop in a channel with a
// contained one. record.mjs's cloud clip is one take, which the first beats
// cut on its marks; the cloud-talk clip is the last beat's.
const C = marks.cloud
const whole: Focus = { x: PAGE.w / 2, y: PAGE.h / 2, zoom: 1 }
const shot = {
  width: MAIN.width,
  url: 'localhost:8080/new',
  style: mainScreen,
  src: 'cloud.mp4',
  camera: whole,
}

// Beat 1: what the video is about.
function Title() {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const o = interpolate(frame, [0, 14, durationInFrames - 14, durationInFrames], [0, 1, 1, 0], clamp)
  const y = interpolate(frame, [0, 20], [12, 0], { ...clamp, easing: Easing.out(Easing.cubic) })
  return (
    <Stage>
      <AbsoluteFill
        style={{ opacity: o, justifyContent: 'center', padding: '0 240px', transform: `translateY(${y}px)` }}
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
          Loops in Claude Code cloud sessions,
          <br />
          <span style={{ color: T.muted }}>beside your contained ones.</span>
        </div>
      </AbsoluteFill>
    </Stage>
  )
}

// Beat 2: the form fills fast, the runtime is chosen at near pace so its
// note can be read, and the reach for Create plays fast up to the page.
const FILL_RATE = 2.5
const FILL_LEN = (C['scroll-runtime'] - C.ready) / FILL_RATE
const PICK_RATE = 1.5
const PICK_END = C.cloud + 1.6
const PICK_LEN = (PICK_END - C['scroll-runtime']) / PICK_RATE
const GO_RATE = 2
const GO_LEN = (C.page - PICK_END) / GO_RATE
const CREATE_LEN = FILL_LEN + PICK_LEN + GO_LEN
function Create() {
  return (
    <Stage>
      <Sequence durationInFrames={s(FILL_LEN)}>
        <Screen {...shot} from={C.ready} rate={FILL_RATE} />
      </Sequence>
      <Sequence from={s(FILL_LEN)} durationInFrames={s(PICK_LEN)}>
        <Screen {...shot} from={C['scroll-runtime']} rate={PICK_RATE} />
      </Sequence>
      <Sequence from={s(FILL_LEN + PICK_LEN)}>
        <Screen {...shot} from={PICK_END} rate={GO_RATE} />
      </Sequence>
      <Caption step="01 · Create">Pick the cloud runtime: it runs on Anthropic&apos;s machines.</Caption>
    </Stage>
  )
}

// Beat 3: the loop's page at pace, from its arrival to the end of the take.
const RUN_LEN = C.end - C.page
function Run() {
  return (
    <Stage>
      <Screen {...shot} url="localhost:8080/loops/triage" from={C.page} />
      <Caption step="02 · Run">Its page says where it runs, and its answers land here.</Caption>
    </Stage>
  )
}

// Beat 4: the cloud loop in a channel with a contained one, on its page,
// beside the Telegram group that carries it: the operator asks triage, and
// triage hands the question to gardener. Each line lands on the cue
// record.mjs revealed it.
const K = marks['cloud-talk']
const talkTimes = marks.times['cloud-talk']
const TALK_LEN = K.line2 + 3 - K.ready
function Talk() {
  return (
    <Pair
      step="03 · Talk"
      caption="Talk to it in Telegram or Slack, and it works with every loop."
      url="localhost:8080/loops/triage?channel=handbook"
      src="cloud-talk.mp4"
      from={K.ready}
      page={TALK_PAGE}
      camera={{ x: TALK_PAGE.w / 2, y: TALK_PAGE.h / 2, zoom: 1 }}
      chat={{
        title: 'Handbook',
        subtitle: 'Telegram group · 3 members',
        avatar: 'H',
        tint: '#4f8e6e',
        typing: { who: 'gardener', from: s(K.line1 - K.ready + 0.8), to: s(K.line2 - K.ready), before: 2 },
        lines: [
          {
            at: s(K.line0 - K.ready),
            who: 'you',
            me: true,
            time: talkTimes.line0,
            text: '@triage anything on the handbook that needs me?',
          },
          {
            at: s(K.line1 - K.ready),
            who: 'triage',
            time: talkTimes.line1,
            text: 'One: #215 wants Windows on the install page. @gardener is that page yours?',
          },
          {
            at: s(K.line2 - K.ready),
            who: 'gardener',
            time: talkTimes.line2,
            text: "Mine. I'll add a Windows section today and link it on #215.",
          },
        ],
      }}
    />
  )
}

const LENGTHS = [2.5, CREATE_LEN, RUN_LEN, TALK_LEN]
const SPANS = LENGTHS.map((len, i) => {
  const from = LENGTHS.slice(0, i).reduce((a, b) => a + b, 0)
  return [from, from + len]
})
export const CLOUD_DURATION = s(SPANS[SPANS.length - 1][1])

const BEATS = [Title, Create, Run, Talk]

export const CloudDemo = () => (
  <AbsoluteFill style={{ background: T.canvas }}>
    {BEATS.map((B, i) => (
      <Beat key={i} span={SPANS[i]}>
        <B />
      </Beat>
    ))}
  </AbsoluteFill>
)

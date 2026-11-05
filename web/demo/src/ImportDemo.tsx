import { AbsoluteFill, Easing, interpolate, Sequence, useCurrentFrame, useVideoConfig } from 'remotion'
import marks from '../public/marks.json'
import {
  Beat,
  Caption,
  clamp,
  MAIN,
  mainScreen,
  PAGE,
  s,
  Screen,
  Stage,
  T,
  Wordmark,
  type Focus,
} from './SpoolDemo'

// The prompts.chat import video (#1326), beside the README's other two: the
// same frame, short. A title, prompts.chat's own lists, a bot's page filling New loop, a team's page
// making three loops and their channel, then the team on Fleet.
// record.mjs's import-bot and import-team clips are one take each, which
// the beats cut on their marks.
const B = marks['import-bot']
const M = marks['import-team']
const whole: Focus = { x: PAGE.w / 2, y: PAGE.h / 2, zoom: 1 }
const shot = { width: MAIN.width, url: 'localhost:8080/new', style: mainScreen, camera: whole }

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
          A bot or a whole team from prompts.chat,
          <br />
          <span style={{ color: T.muted }}>running on your own hub.</span>
        </div>
      </AbsoluteFill>
    </Stage>
  )
}

// Beat 2: prompts.chat itself, at pace: its bots, its teams, and one
// team's page, whose address is what the import takes. The site's page
// loads are cut.
const PB = marks['prompts-chat-bots']
const PT = marks['prompts-chat-teams']
const BOTS_LEN = PB.end - PB.ready
const TEAMS_LEN = PT.click + 0.3 - PT.ready
const FIND_LEN = BOTS_LEN + TEAMS_LEN + (PT.end - PT.team)
function Find() {
  return (
    <Stage>
      <Sequence durationInFrames={s(BOTS_LEN)}>
        <Screen {...shot} url="prompts.chat/bots" src="prompts-chat-bots.mp4" from={PB.ready} />
      </Sequence>
      <Sequence from={s(BOTS_LEN)} durationInFrames={s(TEAMS_LEN)}>
        <Screen {...shot} url="prompts.chat/bots/teams" src="prompts-chat-teams.mp4" from={PT.ready} />
      </Sequence>
      <Sequence from={s(BOTS_LEN + TEAMS_LEN)}>
        <Screen
          {...shot}
          url="prompts.chat/bots/teams/approval-gated-build"
          src="prompts-chat-teams.mp4"
          from={PT.team}
        />
      </Sequence>
      <Caption step="01 · Find">Pick a bot or a team on prompts.chat.</Caption>
    </Stage>
  )
}

// Beat 3: the address is pasted, a little fast; the fill plays at pace so the
// fields can be read, and the egress host's Add at pace too.
const PASTE_RATE = 1.5
const PASTE_LEN = (B.import - B.ready) / PASTE_RATE
const BOT_LEN = PASTE_LEN + (B.end - B.import)
function Bot() {
  return (
    <Stage>
      <Sequence durationInFrames={s(PASTE_LEN)}>
        <Screen {...shot} src="import-bot.mp4" from={B.ready} rate={PASTE_RATE} />
      </Sequence>
      <Sequence from={s(PASTE_LEN)}>
        <Screen {...shot} src="import-bot.mp4" from={B.import} />
      </Sequence>
      <Caption step="02 · A bot">Paste its page: the form fills, for you to edit.</Caption>
    </Stage>
  )
}

// Beat 4: the team's preview at pace, the reach for Create fast, and its
// channel at pace once it exists.
const T_PASTE_LEN = (M.import - M.ready) / PASTE_RATE
const READ_LEN = M['create-click'] - M.import
const GO_RATE = 1.5
const GO_LEN = (M.channel + 2.4 - M['create-click']) / GO_RATE
const TEAM_LEN = T_PASTE_LEN + READ_LEN + GO_LEN
function Team() {
  return (
    <Stage>
      <Sequence durationInFrames={s(T_PASTE_LEN)}>
        <Screen {...shot} src="import-team.mp4" from={M.ready} rate={PASTE_RATE} />
      </Sequence>
      <Sequence from={s(T_PASTE_LEN)} durationInFrames={s(READ_LEN)}>
        <Screen {...shot} src="import-team.mp4" from={M.import} />
      </Sequence>
      <Sequence from={s(T_PASTE_LEN + READ_LEN)}>
        <Screen
          {...shot}
          url="localhost:8080/channels#approval-gated-build"
          src="import-team.mp4"
          from={M['create-click']}
          rate={GO_RATE}
        />
      </Sequence>
      <Caption step="03 · A team">A team&apos;s page makes every loop and the channel they share.</Caption>
    </Stage>
  )
}

// Beat 5: the team on Fleet, beside the loops already running.
const FLEET_LEN = M.end - M.fleet
function Fleet() {
  return (
    <Stage>
      <Screen {...shot} url="localhost:8080/" src="import-team.mp4" from={M.fleet} />
      <Caption step="04 · Run">They join your fleet, each handing its work to the next.</Caption>
    </Stage>
  )
}

const LENGTHS = [2.5, FIND_LEN, BOT_LEN, TEAM_LEN, FLEET_LEN]
const SPANS = LENGTHS.map((len, i) => {
  const from = LENGTHS.slice(0, i).reduce((a, b) => a + b, 0)
  return [from, from + len]
})
export const IMPORT_DURATION = s(SPANS[SPANS.length - 1][1])
// Where each beat starts, for stills.
export const IMPORT_SPANS = SPANS

const BEATS = [Title, Find, Bot, Team, Fleet]

export const ImportDemo = () => (
  <AbsoluteFill style={{ background: T.canvas }}>
    {BEATS.map((Beat_, i) => (
      <Beat key={i} span={SPANS[i]}>
        <Beat_ />
      </Beat>
    ))}
  </AbsoluteFill>
)

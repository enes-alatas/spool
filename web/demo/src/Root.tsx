import { Composition } from 'remotion'
import { SpoolDemo, DURATION, FPS, W, H } from './SpoolDemo'

export const Root = () => (
  <Composition
    id="SpoolDemo"
    component={SpoolDemo}
    durationInFrames={DURATION}
    fps={FPS}
    width={W}
    height={H}
  />
)

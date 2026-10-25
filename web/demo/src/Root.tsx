import { Composition } from 'remotion'
import { SpoolDemo, DURATION, FPS, W, H } from './SpoolDemo'
import { CloudDemo, CLOUD_DURATION } from './CloudDemo'

export const Root = () => (
  <>
    <Composition
      id="SpoolDemo"
      component={SpoolDemo}
      durationInFrames={DURATION}
      fps={FPS}
      width={W}
      height={H}
    />
    <Composition
      id="CloudDemo"
      component={CloudDemo}
      durationInFrames={CLOUD_DURATION}
      fps={FPS}
      width={W}
      height={H}
    />
  </>
)

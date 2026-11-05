import { Composition } from 'remotion'
import { SpoolDemo, DURATION, FPS, W, H } from './SpoolDemo'
import { CloudDemo, CLOUD_DURATION } from './CloudDemo'
import { ImportDemo, IMPORT_DURATION } from './ImportDemo'

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
    <Composition
      id="ImportDemo"
      component={ImportDemo}
      durationInFrames={IMPORT_DURATION}
      fps={FPS}
      width={W}
      height={H}
    />
  </>
)

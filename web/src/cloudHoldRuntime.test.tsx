import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { CloudSettings, LoopView } from './api'
import { unnamedCloudHold, useCloudSettings } from './cloudLoop'

// Settings → Cloud with no address, as New loop or Settings leaves it in
// the cache on a hub with no cloud setup (#1492).
const noAddress: CloudSettings = {
  listener: { on: false, addr: '', from_flag: false },
  address: '',
  account: { set: false, cli: false },
}

function Hold({ loop }: { loop: Pick<LoopView, 'runtime' | 'problems'> }) {
  return <>{unnamedCloudHold(loop, useCloudSettings(loop)) || 'none'}</>
}

const hold = (runtime: LoopView['runtime']) => {
  const qc = new QueryClient()
  qc.setQueryData(['cloud'], noAddress)
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}>
      <Hold loop={{ runtime, problems: [] }} />
    </QueryClientProvider>,
  )
}

describe('useCloudSettings', () => {
  it("holds no loop but a cloud one on Settings → Cloud's gaps, whatever the cache holds", () => {
    expect(hold('docker')).toBe('none')
    expect(hold('bare')).toBe('none')
    expect(hold('cloud')).toBe('no public address is set')
  })
})

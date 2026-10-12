import { linkRuns } from '../linkify'

// Text with its web addresses made clickable (#759), and nothing else read
// from it. Each link opens in a new tab and carries no opener or referrer,
// so a page a loop pointed at cannot reach back into the room.
export function Linked({ text }: { text: string }) {
  return (
    <>
      {linkRuns(text).map((run, i) =>
        run.kind === 'link' ? (
          <a key={i} className="text-link" href={run.href} target="_blank" rel="noopener noreferrer">
            {run.href}
          </a>
        ) : (
          run.text
        ),
      )}
    </>
  )
}

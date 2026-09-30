// The spool glyph: a thread spool seen from the side. Spins while busy.
// Drawn to the README banner's proportions (#456): hairline rings, the inner
// one about 0.36 of the outer, and spokes running from rim to hub. The stroke
// is heavier than the banner's own ratio, which would vanish at 26px.
export function SpoolGlyph({ size = 18, spinning = false }: { size?: number; spinning?: boolean }) {
  return (
    <svg
      className={`spool-glyph${spinning ? ' spinning' : ''}`}
      width={size}
      height={size}
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth="0.9"
      aria-hidden
    >
      <circle cx="10" cy="10" r="8.7" />
      <circle cx="10" cy="10" r="3.1" />
      <line x1="10" y1="1.3" x2="10" y2="6.9" />
      <line x1="10" y1="13.1" x2="10" y2="18.7" />
    </svg>
  )
}

// The dot is an accent on a state that is already written next to it, so it
// carries no name of its own: a title here is read out after the word and
// says it twice. Anywhere the dot would stand alone, the name has to come
// with it — see the sender rows on Access.
export function StateDot({ state }: { state: string }) {
  return <span className={`state-dot state-${state}`} />
}

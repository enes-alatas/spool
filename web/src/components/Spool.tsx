// The spool glyph: a thread spool seen from the side. Spins while busy.
export function SpoolGlyph({ size = 18, spinning = false }: { size?: number; spinning?: boolean }) {
  return (
    <svg
      className={`spool-glyph${spinning ? ' spinning' : ''}`}
      width={size}
      height={size}
      viewBox="0 0 20 20"
      fill="none"
      aria-hidden
    >
      <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.6" />
      <circle cx="10" cy="10" r="3.2" stroke="currentColor" strokeWidth="1.4" />
      <line x1="10" y1="1.6" x2="10" y2="6.4" stroke="currentColor" strokeWidth="1.4" />
      <line x1="10" y1="13.6" x2="10" y2="18.4" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  )
}

export function StateDot({ state }: { state: string }) {
  return <span className={`state-dot state-${state}`} title={state} />
}

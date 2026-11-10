// FleetColumns is the Fleet list's heading row: one head per column of the
// rows below it. The heads are written in sentence case here, not cased by
// the stylesheet: they sit at the rows' own size with no transform (#1406),
// so the text on the page is the text in this file.
export function FleetColumns() {
  return (
    <div className="fleet-cols" aria-hidden>
      <span />
      <span>Loop</span>
      <span className="f-state">State</span>
      <div className="f-meta">
        <span className="f-next">Next wake</span>
        <span className="f-today">Today</span>
      </div>
    </div>
  )
}

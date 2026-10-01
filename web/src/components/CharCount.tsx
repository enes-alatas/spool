// How much of a field's limit is used, next to the field. It turns the danger
// colour past the limit, so the operator sees the wall before the hub refuses.
export function CharCount({ used, max }: { used: number; max: number }) {
  return <span className={`char-count${used > max ? ' over' : ''}`}>{`${used} / ${max}`}</span>
}

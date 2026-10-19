import { Fragment } from 'react'
import { splitMarked } from '../marked'
import { Copyable } from './Copyable'

// A sentence the hub wrote for the room, an error or a status reason, with
// each literal it marked drawn as a chip that copies itself (#870, #871).
export function Marked({ text }: { text: string }) {
  return (
    <>
      {splitMarked(text).map((part, i) =>
        part.literal ? <Copyable key={i} text={part.text} /> : <Fragment key={i}>{part.text}</Fragment>,
      )}
    </>
  )
}

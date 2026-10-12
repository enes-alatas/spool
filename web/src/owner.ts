// The loop page's owner picker (#732): an owner is a user, picked by the
// link they hold on the loop's surface (ADR-0051, #731). The surface's
// senders list says which user each sender is linked as; a sender allowed
// before links is linked as nobody and is not offered.

export interface OwnerPerson {
  // The user the sender is linked as, "" for one allowed before links.
  user: string
  status: 'pending' | 'allowed' | 'blocked'
  // How the person is known on the surface: "@rana_example".
  handle: string
}

export interface OwnerChoice {
  value: string
  label: string
  disabled?: boolean
}

// The value the picker holds for an owner no user is linked as. It can be
// shown but not picked: nothing in the request names a sender any more.
export const NOT_LINKED = ':not-linked'

// "rana · @rana_example": the user first, since that is who is picked, and
// the handle after it, since that is who the loop will write to.
function choiceLabel(person: OwnerPerson): string {
  return person.handle ? `${person.user} · ${person.handle}` : person.user
}

// What the picker offers and which of it is selected. Only an allowed sender
// linked as a user is offered. The current owner stays in the list even when
// they could not be picked today, blocked or linked as nobody: a picker that
// drops the value it shows reads as "no owner".
export function ownerChoices(
  people: OwnerPerson[],
  owner: { set: boolean; user: string; handle: string },
): { value: string; choices: OwnerChoice[]; offered: number } {
  const choices: OwnerChoice[] = []
  const seen = new Set<string>()
  for (const person of people) {
    if (!person.user || person.status !== 'allowed' || seen.has(person.user)) continue
    seen.add(person.user)
    choices.push({ value: person.user, label: choiceLabel(person) })
  }
  const offered = choices.length
  if (!owner.set) {
    return { value: '', choices: [{ value: '', label: 'No owner set' }, ...choices], offered }
  }
  if (!owner.user) {
    const label = `${owner.handle || 'a sender'} · not linked`
    return { value: NOT_LINKED, choices: [...choices, { value: NOT_LINKED, label, disabled: true }], offered }
  }
  if (!seen.has(owner.user)) {
    const person = people.find((p) => p.user === owner.user)
    choices.push({
      value: owner.user,
      label: choiceLabel(person ?? { user: owner.user, status: 'allowed', handle: owner.handle }),
    })
  }
  return { value: owner.user, choices, offered }
}

// One restrained line-icon family for the primary destinations. Local on
// purpose: a navigation bar is not worth a dependency (CONVENTIONS.md,
// stdlib-first). Every glyph is drawn in the same 20px viewBox with the same
// 1.5 stroke so the row reads as one set, and renders at 16px.
import type { ReactNode } from 'react'

function Icon({ children }: { children: ReactNode }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {children}
    </svg>
  )
}

// Fleet: loops stacked as rows, each with its state marker.
export function FleetIcon() {
  return (
    <Icon>
      <rect x="2.75" y="4" width="14.5" height="4.5" rx="1.25" />
      <rect x="2.75" y="11.5" width="14.5" height="4.5" rx="1.25" />
      <path d="M5.75 6.25h.01M5.75 13.75h.01" />
    </Icon>
  )
}

// Activity: the fleet's pulse over time.
export function ActivityIcon() {
  return (
    <Icon>
      <path d="M2.5 10h3l2.25-5 3.5 10 2.25-5h3.5" />
    </Icon>
  )
}

// Connections: a plug — a tool the fleet is wired to.
export function ConnectionsIcon() {
  return (
    <Icon>
      <path d="M7.5 2.75v3.5M12.5 2.75v3.5" />
      <path d="M5 6.25h10v3.25a5 5 0 0 1-10 0z" />
      <path d="M10 14.5v2.75" />
    </Icon>
  )
}

// Access: a key — who may reach the room.
export function AccessIcon() {
  return (
    <Icon>
      <circle cx="7" cy="7" r="3.75" />
      <path d="M9.65 9.65 16.5 16.5M14.25 14.25l-1.75 1.75M16.5 12l-1.75 1.75" />
    </Icon>
  )
}

// Rules: the standing text every loop is handed.
export function RulesIcon() {
  return (
    <Icon>
      <path d="M4.5 3.25h11v13.5h-11z" />
      <path d="M7.25 7h5.5M7.25 10h5.5M7.25 13h3" />
    </Icon>
  )
}

// Settings: sliders, not a cog — these are values you set.
export function SettingsIcon() {
  return (
    <Icon>
      <path d="M3 6.5h4.5M11 6.5h6M3 13.5h6M12.5 13.5h4.5" />
      <circle cx="9.25" cy="6.5" r="1.75" />
      <circle cx="10.75" cy="13.5" r="1.75" />
    </Icon>
  )
}

// Edit: a pen. Not a destination like the icons above, but drawn in the same
// family so a panel's action does not read as a different kind of object
// from the navigation it sits beside.
export function EditIcon() {
  return (
    <Icon>
      <path d="M13.1 3.65a1.9 1.9 0 0 1 2.7 2.7l-8.3 8.3-3.4.7.7-3.4z" />
      <path d="M11.9 4.85l2.7 2.7" />
    </Icon>
  )
}

// Attach: a paperclip, for the composer's file picker.
export function AttachIcon() {
  return (
    <Icon>
      <path d="M15.5 9.25 9.6 15.15a3.6 3.6 0 0 1-5.1-5.1l6.2-6.2a2.4 2.4 0 0 1 3.4 3.4l-6.15 6.15a1.2 1.2 0 0 1-1.7-1.7l5.6-5.6" />
    </Icon>
  )
}

// Channels: the hash a channel is named with.
export function ChannelsIcon() {
  return (
    <Icon>
      <path d="M8 3.5 6.5 16.5M13.5 3.5 12 16.5M3.75 7.5h13M3.25 12.5h13" />
    </Icon>
  )
}

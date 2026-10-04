// One restrained line-icon family for the primary destinations. Local on
// purpose: a navigation bar is not worth a dependency (CONVENTIONS.md,
// stdlib-first). Every glyph is drawn in the same 20px viewBox with the same
// 1.5 stroke so the row reads as one set, and renders at 16px.
import type { ReactNode } from 'react'

// The size defaults to the nav's 16px; the first-run page draws its pillars
// larger from the same glyphs.
function Icon({ children, size = 16 }: { children: ReactNode; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
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

// Add: a plus, for a panel that adds to its list (a loop's connections).
export function PlusIcon() {
  return (
    <Icon>
      <path d="M10 4v12M4 10h12" />
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

// The first-run pillars (#581). Harness: a terminal prompt — Claude Code
// running on this machine.
export function HarnessIcon({ size }: { size?: number }) {
  return (
    <Icon size={size}>
      <rect x="2.5" y="3.75" width="15" height="12.5" rx="1.75" />
      <path d="M6 8.25l2.25 1.75L6 11.75M10.25 12h3.5" />
    </Icon>
  )
}

// Chat surface: a speech bubble — where the operator talks to the fleet.
export function SurfaceIcon({ size }: { size?: number }) {
  return (
    <Icon size={size}>
      <path d="M3.25 5.25a1.75 1.75 0 0 1 1.75-1.75h10a1.75 1.75 0 0 1 1.75 1.75v6.5A1.75 1.75 0 0 1 15 13.5H8.5l-3.75 3v-3h0A1.75 1.75 0 0 1 3.25 11.75z" />
      <path d="M7 8.5h.01M10 8.5h.01M13 8.5h.01" />
    </Icon>
  )
}

// Loops: the spool itself, in this family's stroke.
export function LoopsIcon({ size }: { size?: number }) {
  return (
    <Icon size={size}>
      <circle cx="10" cy="10" r="7.25" />
      <circle cx="10" cy="10" r="2.5" />
      <path d="M10 2.75v4.75M10 12.5v4.75" />
    </Icon>
  )
}

// Done: a check, beside the words that say so.
export function CheckIcon({ size }: { size?: number }) {
  return (
    <Icon size={size}>
      <path d="M4.5 10.5l3.5 3.5 7.5-8" />
    </Icon>
  )
}

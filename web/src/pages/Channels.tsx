import { useState } from 'react'
import { useMay } from '../components/Session'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, Channel } from '../api'
import { CharCount } from '../components/CharCount'
import {
  CHANNEL_DESCRIPTION_MAX,
  CHANNEL_NAME_MAX,
  FLEET_CHANNEL,
  descriptionLength,
  loopsOutside,
  validChannelName,
} from '../channels'

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

// Edits a channel's description in place of the line that shows it.
function DescriptionEditor({
  channel,
  onDone,
  onCancel,
}: {
  channel: Channel
  onDone: () => void
  onCancel: () => void
}) {
  const [description, setDescription] = useState(channel.description)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const tooLong = descriptionLength(description) > CHANNEL_DESCRIPTION_MAX

  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await api.patchChannel(channel.name, { description: description.trim() })
      onDone()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rule-editor">
      <div className="rule-field">
        <label htmlFor={`desc-${channel.name}`}>description</label>
        <CharCount used={descriptionLength(description)} max={CHANNEL_DESCRIPTION_MAX} />
      </div>
      <input
        id={`desc-${channel.name}`}
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="What it is for"
      />
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
      <div className="controls" style={{ marginTop: 8 }}>
        <button className="btn primary" onClick={save} disabled={busy || tooLong}>
          {busy ? 'Saving…' : 'Save'}
        </button>
        <button className="btn" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  )
}

function ChannelCard({
  channel,
  loops,
  refresh,
}: {
  channel: Channel
  loops: string[]
  refresh: () => void
}) {
  const [editing, setEditing] = useState(false)
  // Editing a channel and who is in it are an admin's (#679).
  const manage = useMay()('manage')
  // A refusal is anchored to the card it came from, not the page top, where
  // with several channels on screen it would scroll out of sight.
  const [error, setError] = useState('')
  const fleet = channel.name === FLEET_CHANNEL
  const addable = loopsOutside(channel.loops, loops)

  const act = async (run: () => Promise<unknown>) => {
    setError('')
    try {
      await run()
    } catch (err) {
      setError(errorText(err))
    }
    refresh()
  }

  return (
    <div className="rule-item">
      <div className="rule-head">
        <span className="channel-name">#{channel.name}</span>
        {fleet && <span className="rule-badge">fleet channel</span>}
        <span className="channel-count">
          {channel.loops.length} loop{channel.loops.length === 1 ? '' : 's'}
        </span>
        {/* The fleet channel always exists, and what it is for is fixed:
            there is nothing to edit or delete, only who is in it. */}
        {!fleet && manage && (
          <span className="rule-actions">
            <button className="btn sm" onClick={() => setEditing(!editing)}>
              {editing ? 'Close' : 'Edit'}
            </button>
            <button
              className="btn sm danger"
              onClick={() => {
                if (
                  confirm(`Delete #${channel.name}? Its loops leave it, and it drops out of their prompts.`)
                ) {
                  act(() => api.deleteChannel(channel.name))
                }
              }}
            >
              Delete
            </button>
          </span>
        )}
      </div>
      {editing ? (
        <DescriptionEditor
          channel={channel}
          onDone={() => {
            setEditing(false)
            refresh()
          }}
          onCancel={() => setEditing(false)}
        />
      ) : fleet ? (
        <div className="rule-body">
          The fleet channel. Every loop is in it unless {manage ? 'you take it out' : 'an admin takes it out'}
          .
        </div>
      ) : channel.description ? (
        <div className="rule-body">{channel.description}</div>
      ) : (
        <div className="rule-body channel-undescribed">No description</div>
      )}
      <div className="channel-loops">
        {channel.loops.map((loop) => (
          <span key={loop} className="channel-loop">
            @{loop}
            {manage && (
              <button
                aria-label={`Take @${loop} out of #${channel.name}`}
                title={`Take @${loop} out of #${channel.name}`}
                onClick={() => act(() => api.removeChannelLoop(channel.name, loop))}
              >
                ×
              </button>
            )}
          </span>
        ))}
        {/* A pick-list whose first option is its label: choosing a loop puts
            it in at once, and the list snaps back to the label. */}
        {manage && addable.length > 0 && (
          <select
            className="channel-add"
            value=""
            aria-label={`Add a loop to #${channel.name}`}
            onChange={(e) => act(() => api.addChannelLoop(channel.name, e.target.value))}
          >
            <option value="" disabled>
              + Add loop
            </option>
            {addable.map((loop) => (
              <option key={loop} value={loop}>
                @{loop}
              </option>
            ))}
          </select>
        )}
      </div>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

function NewChannel({ onCreated }: { onCreated: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  // Said once a name is typed, not before: an empty field isn't a mistake.
  const badName = name !== '' && !validChannelName(name)
  const tooLong = descriptionLength(description) > CHANNEL_DESCRIPTION_MAX

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      await api.createChannel({ name, description: description.trim() })
      setName('')
      setDescription('')
      onCreated()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="channel-new">
      <h2 className="section-head">New channel</h2>
      <div className="channel-new-row">
        <input
          className="channel-name-input"
          value={name}
          onChange={(e) => {
            setName(e.target.value)
            // A refusal was about the name it came back for, not this one.
            setError('')
          }}
          maxLength={CHANNEL_NAME_MAX}
          placeholder="name"
          aria-label="Channel name"
          aria-invalid={badName}
          aria-describedby="channel-name-rule"
          autoComplete="off"
          spellCheck={false}
        />
        <input
          className="channel-description-input"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What it is for (optional)"
          aria-label="Description"
        />
        <button className="btn primary" onClick={create} disabled={busy || !name || badName || tooLong}>
          {busy ? 'Creating…' : 'Create'}
        </button>
      </div>
      <div id="channel-name-rule" className={`channel-note${badName ? ' bad' : ''}`}>
        Lowercase letters, digits and dashes, not starting with a dash. The name can't be changed later.
        {tooLong && ` The description is over ${CHANNEL_DESCRIPTION_MAX} characters.`}
      </div>
      {error && (
        <div className="form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  )
}

export default function Channels() {
  const qc = useQueryClient()
  const may = useMay()
  const channels = useQuery({ queryKey: ['channels'], queryFn: api.channels })
  const loops = useQuery({ queryKey: ['loops'], queryFn: api.loops })

  // A change to `group` is a change to each loop's in_fleet_channel, which
  // the fleet and the loop pages read, so they refetch along with the list.
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['channels'] })
    qc.invalidateQueries({ queryKey: ['loops'] })
    qc.invalidateQueries({ queryKey: ['loop'] })
  }

  if (channels.isPending) return <div className="page measure placeholder">Loading…</div>
  if (!channels.data) {
    // A hub from before channels has no such route: that is a hub to update,
    // not a failure to report by status code.
    const missing = channels.error instanceof ApiError && channels.error.status === 404
    return (
      <div className="page measure">
        <h1>Channels</h1>
        <div className="form-error" role="alert">
          {missing
            ? 'This orchestrator has no channels API. Update Spool to manage channels from here.'
            : `Could not load channels: ${errorText(channels.error)}`}
        </div>
      </div>
    )
  }

  const loopNames = (loops.data ?? []).map((loop) => loop.name)
  return (
    <div className="page measure">
      <h1>Channels</h1>
      <p className="page-lede">
        {may('manage') &&
          'Set up channels here: create one, say what it is for, and choose which loops are in it. '}
        The loops in a channel read and post to it, and each is told the channels it is in. For now a channel
        other than the fleet channel stays in Spool: its messages show in Activity, and no Telegram or Slack
        room carries them yet.
      </p>
      {channels.data.map((channel) => (
        <ChannelCard key={channel.name} channel={channel} loops={loopNames} refresh={refresh} />
      ))}
      {may('manage') && <NewChannel onCreated={refresh} />}
    </div>
  )
}

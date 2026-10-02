import type { LoopView, MCPServer } from '../api'

// What the loop's session reached through MCP (#489, #519): each server its
// init reported, in its order, with the CLI's status word and its tool count,
// and the session the list is from. Nothing is summed away, so a second
// server or a failed one shows as itself. Until a session starts in this hub
// run there is no init to read, and the row says so rather than guess.
export function MCPReach({ loop }: { loop: LoopView }) {
  const servers = loop.mcp_servers
  return (
    <div className="row mcp-reach">
      <span className="k">mcp</span>
      {servers === undefined ? (
        <span className="v mcp-pending" title="A session reports it when it starts in this hub run">
          not reported yet
        </span>
      ) : (
        <span className="v mcp-servers">
          {servers.length === 0 && <span>no servers</span>}
          {servers.map((server) => (
            <ServerLine key={server.name} server={server} />
          ))}
          {loop.mcp_session_id && (
            <span className="mcp-session">
              session {loop.mcp_session_id.slice(0, 8)}
              {loop.current_session_id &&
                loop.current_session_id !== loop.mcp_session_id &&
                ', not the current one'}
            </span>
          )}
        </span>
      )}
    </div>
  )
}

function ServerLine({ server }: { server: MCPServer }) {
  const tone = server.status === 'connected' ? 'ok' : server.status === 'failed' ? 'bad' : ''
  return (
    <span>
      {server.name} <span className={tone}>({server.status})</span> · {server.tool_count}{' '}
      {server.tool_count === 1 ? 'tool' : 'tools'}
    </span>
  )
}

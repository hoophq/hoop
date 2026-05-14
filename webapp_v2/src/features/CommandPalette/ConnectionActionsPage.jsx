import { SpotlightAction, SpotlightActionsGroup } from '@mantine/spotlight'
import { SquareCode, FlaskConical, Settings } from 'lucide-react'

const ACTION_TYPES = {
  WEB_TERMINAL: 'web-terminal',
  HOOP_CLI: 'hoop-cli',
  NATIVE_CLIENT: 'native-client',
  TEST: 'test',
  CONFIGURE: 'configure',
}

const DIRECT_NATIVE_SUBTYPES = new Set(['postgres', 'ssh', 'github', 'git'])
const HTTP_PROXY_SUBTYPES = new Set(['httpproxy', 'kibana', 'grafana', 'claude-code'])
const CUSTOM_NATIVE_SUBTYPES = new Set(['rdp', 'aws-ssm'])
const KUBERNETES_SUBTYPES = new Set(['kubernetes-token', 'kubernetes', 'kubernetes-eks'])

function canAccessNativeClient(connection) {
  if (!connection || connection.access_mode_connect !== 'enabled') return false
  const { subtype, type } = connection
  return (
    DIRECT_NATIVE_SUBTYPES.has(subtype) ||
    HTTP_PROXY_SUBTYPES.has(subtype) ||
    KUBERNETES_SUBTYPES.has(subtype) ||
    (type === 'custom' && CUSTOM_NATIVE_SUBTYPES.has(subtype))
  )
}

export default function ConnectionActionsPage({ connection, resource, isAdmin, onAction }) {
  return (
    <>
      <SpotlightActionsGroup label={`Actions for ${connection?.name || 'connection'}`}>
        <SpotlightAction
          label="Open in Web Terminal"
          description="Open in browser terminal"
          leftSection={<SquareCode size={16} />}
          onClick={() => onAction(ACTION_TYPES.WEB_TERMINAL, connection, resource)}
        />
        {canAccessNativeClient(connection) && (
          <SpotlightAction
            label="Open in Native Client"
            description="Connect with native client"
            leftSection={<SquareCode size={16} />}
            onClick={() => onAction(ACTION_TYPES.NATIVE_CLIENT, connection, resource)}
          />
        )}
        {isAdmin && (
          <SpotlightAction
            label="Test Connection"
            description="Run a connectivity test"
            leftSection={<FlaskConical size={16} />}
            onClick={() => onAction(ACTION_TYPES.TEST, connection, resource)}
          />
        )}
      </SpotlightActionsGroup>
      {isAdmin && (
        <SpotlightActionsGroup label="Settings">
          <SpotlightAction
            label="Configure"
            description="Edit connection settings"
            leftSection={<Settings size={16} />}
            onClick={() => onAction(ACTION_TYPES.CONFIGURE, connection, resource)}
          />
        </SpotlightActionsGroup>
      )}
    </>
  )
}

export { ACTION_TYPES }

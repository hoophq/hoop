import { SpotlightAction, SpotlightActionsGroup } from '@mantine/spotlight'
import { spotlight } from '@mantine/spotlight'
import { useNavigate } from 'react-router-dom'
import { SquareCode, Terminal, FlaskConical, Settings, Copy } from 'lucide-react'
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore'
import { useUserStore } from '@/stores/useUserStore'
import { notifications } from '@mantine/notifications'

export default function ConnectionActionsPage() {
  const navigate = useNavigate()
  const { context } = useCommandPaletteStore()
  const { user } = useUserStore()
  const connection = context.connection

  const handleCopyLocal = () => {
    const cmd = `hoop connect ${connection?.name}`
    navigator.clipboard.writeText(cmd).then(() => {
      notifications.show({ message: `Copied: ${cmd}`, color: 'green' })
    })
    spotlight.close()
  }

  return (
    <SpotlightActionsGroup label={`Actions for ${connection?.name || 'connection'}`}>
      <SpotlightAction
        label="Web Terminal"
        description="Open in browser terminal"
        leftSection={<SquareCode size={16} />}
        onClick={() => {
          spotlight.close()
          navigate(`/client?connection=${connection?.name}`)
        }}
      />
      <SpotlightAction
        label="Local Terminal"
        description={`Copy: hoop connect ${connection?.name}`}
        leftSection={<Terminal size={16} />}
        onClick={handleCopyLocal}
      />
      {user?.is_admin && (
        <SpotlightAction
          label="Test Connection"
          description="Run a connectivity test"
          leftSection={<FlaskConical size={16} />}
          onClick={() => {
            spotlight.close()
            navigate(`/connections/${connection?.name}/test`)
          }}
        />
      )}
      {user?.is_admin && (
        <SpotlightAction
          label="Configure"
          description="Edit connection settings"
          leftSection={<Settings size={16} />}
          onClick={() => {
            spotlight.close()
            navigate(`/connections/${connection?.name}`)
          }}
        />
      )}
    </SpotlightActionsGroup>
  )
}

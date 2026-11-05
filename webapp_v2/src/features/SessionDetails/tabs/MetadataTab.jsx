import { useNavigate } from 'react-router-dom'
import { Anchor, Group, Stack, Text } from '@mantine/core'
import { PackagePlus } from 'lucide-react'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import InfoRow from '../components/InfoRow'
import { HIDDEN_METADATA_KEYS } from '../sessionState'

const isHttpUrl = (value) => typeof value === 'string' && /^https?:\/\/\S+$/i.test(value)

function Value({ value }) {
  if (isHttpUrl(value)) {
    return (
      <Anchor href={value} target="_blank" rel="noopener noreferrer" size="sm">
        {value}
      </Anchor>
    )
  }
  return <Text size="sm">{typeof value === 'string' ? value : JSON.stringify(value)}</Text>
}

// A sidecar session links to the approvals that held its statements, and a
// review's session to the sidecar session it held.
function SidecarRows({ metadata }) {
  const navigate = useNavigate()
  const showSession = useSessionDetailsStore((s) => s.showSession)
  const close = useSessionDetailsStore((s) => s.close)
  const { name, listener, review_sessions: reviewSessions = [] } = metadata.sidecar || {}
  const sidecarSessionId = metadata.sidecar_session_id
  return (
    <>
      {name && (
        <InfoRow icon={PackagePlus} label="sidecar">
          <Text size="sm">{listener ? `${name} · ${listener}` : name}</Text>
        </InfoRow>
      )}
      {reviewSessions.length > 0 && (
        <InfoRow icon={PackagePlus} label="approvals">
          <Group gap="sm">
            {reviewSessions.map((id) => (
              <Anchor
                key={id}
                component="button"
                size="sm"
                ff="monospace"
                onClick={() => {
                  close()
                  navigate(`/approvals/${encodeURIComponent(id)}`)
                }}
              >
                {id}
              </Anchor>
            ))}
          </Group>
        </InfoRow>
      )}
      {sidecarSessionId && (
        <InfoRow icon={PackagePlus} label="sidecar session">
          <Anchor component="button" size="sm" ff="monospace" onClick={() => showSession(sidecarSessionId)}>
            {sidecarSessionId}
          </Anchor>
        </InfoRow>
      )}
    </>
  )
}

// Figma "Tabs | Metadata": one key and value per row.
export default function MetadataTab({ session }) {
  const metadata = session.metadata || {}
  const entries = Object.entries(metadata).filter(([key]) => !HIDDEN_METADATA_KEYS.has(key))
  return (
    <Stack gap="md">
      <SidecarRows metadata={metadata} />
      {entries.map(([key, value]) => (
        <InfoRow key={key} icon={PackagePlus} label={key}>
          <Value value={value} />
        </InfoRow>
      ))}
    </Stack>
  )
}

import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { Anchor, List, Stack, Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { listenerIndexByLabel, listenerPath } from '../listeners'

function Problems({ items }) {
  return (
    <List size="sm" spacing={4}>
      {items.map((p) => (
        <List.Item key={p}>{p}</List.Item>
      ))}
    </List>
  )
}

/**
 * Why the sidecar would refuse the save, from the 422's problems
 * (listeners.js groupSaveProblems). The page's pinned footer is where the save
 * was clicked, so this scrolls itself into view.
 */
export default function SaveProblems({ refused, sidecarId, listeners }) {
  const ref = useRef(null)
  useEffect(() => {
    if (refused) ref.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [refused])

  if (!refused) return null
  return (
    <Stack gap="sm" ref={ref}>
      {refused.own.length > 0 && (
        <Alert color="red" icon={<TriangleAlert size={16} />} title="The sidecar would refuse this listener">
          <Problems items={refused.own} />
        </Alert>
      )}
      {refused.others.map(({ name, problems }) => {
        const index = listenerIndexByLabel(listeners, name)
        return (
          <Alert key={name} color="red" icon={<TriangleAlert size={16} />} title={`Another listener is invalid: ${name}`}>
            <Stack gap="xs">
              <Text size="sm">Every save checks the whole sidecar configuration, so fix or delete it first.</Text>
              <Problems items={problems} />
              {index !== -1 && (
                <Anchor component={Link} to={listenerPath(sidecarId, listeners[index], index)} size="sm" fw={500}>
                  {`Edit ${name}`}
                </Anchor>
              )}
            </Stack>
          </Alert>
        )
      })}
    </Stack>
  )
}

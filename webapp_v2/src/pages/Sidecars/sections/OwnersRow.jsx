import { useState } from 'react'
import { Group, Skeleton, Stack, Text } from '@mantine/core'
import { Check, SquarePen, TriangleAlert } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Tooltip from '@/components/Tooltip'
import { useSidecarStore } from '@/stores/useSidecarStore'
import OwnersModal from './OwnersModal'

function OwnerStatus({ matched }) {
  if (matched) {
    return (
      <Badge tag chip variant="light" color="green" c="green" icon={<Check size={12} aria-hidden="true" />}>
        Matched
      </Badge>
    )
  }
  return (
    <Tooltip label="No Hoop user has this email, or more than one has it." multiline w={260}>
      <Badge tag chip variant="warning" icon={<TriangleAlert size={12} aria-hidden="true" />}>
        No Hoop user
      </Badge>
    </Tooltip>
  )
}

/** The value side of the Owners row: who answers for this sidecar. Owners are not configuration, so a sidecar running its file edits them too. */
export default function OwnersRow({ sidecar }) {
  const owners = useSidecarStore((s) => s.owners)
  const failed = useSidecarStore((s) => s.ownersError)
  const fetchOwners = useSidecarStore((s) => s.fetchOwners)
  const [editing, setEditing] = useState(false)

  if (failed) {
    return (
      <>
        <Text size="sm" c="red">
          Failed to load owners.
        </Text>
        <Button variant="subtle" size="compact-sm" onClick={() => fetchOwners(sidecar.id)}>
          Retry
        </Button>
      </>
    )
  }
  // Null until the first answer: no list is not the same as an empty one.
  if (!owners) return <Skeleton h={16} w={200} />

  return (
    <>
      {owners.length === 0 ? (
        <Text size="sm">None</Text>
      ) : (
        <Stack gap={4}>
          {owners.map((owner) => (
            <Group key={owner.email} gap="xs" wrap="nowrap">
              <Text size="sm">{owner.email}</Text>
              <OwnerStatus matched={owner.matched} />
            </Group>
          ))}
        </Stack>
      )}
      <Button
        variant="subtle"
        size="compact-sm"
        leftSection={<SquarePen size={14} />}
        onClick={() => setEditing(true)}
      >
        Edit
      </Button>
      <OwnersModal opened={editing} sidecar={sidecar} owners={owners} onClose={() => setEditing(false)} />
    </>
  )
}

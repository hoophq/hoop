import { Anchor, Stack, Text } from '@mantine/core'
import { ArrowLeft, Lock } from 'lucide-react'
import { Link } from 'react-router-dom'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import ConfigureHeader from '@/pages/Roles/Configure/ConfigureHeader'

// A mirror stores no credentials and the API refuses any change to it, so
// there is nothing to edit here.
export default function SidecarMirrorView({ connection, onBack }) {
  return (
    <Stack gap="xl">
      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={onBack}
        px={0}
        w="fit-content"
      >
        Back
      </Button>

      <ConfigureHeader connection={connection} />

      <Alert variant="light" color="gray" icon={<Lock size={16} />}>
        <Text size="sm">
          {'This role mirrors a sidecar listener, and the sidecar\'s configuration owns it. Change it in '}
          <Anchor component={Link} to="/sidecars" size="sm">
            Sidecars
          </Anchor>
          {'.'}
        </Text>
      </Alert>

      <Stack gap="xl" maw={720}>
        <TextInput label="Name" value={connection.name} disabled />
        <TextInput label="Resource" value={connection.resource_name ?? ''} disabled />
        <TextInput label="Protocol" value={connection.subtype || connection.type || ''} disabled />
      </Stack>
    </Stack>
  )
}

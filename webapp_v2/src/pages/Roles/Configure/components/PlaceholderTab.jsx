import { Alert, Stack, Text, Button } from '@mantine/core'
import { Info, ExternalLink } from 'lucide-react'

// Placeholder for tabs that exist in the CLJS form but aren't ported to
// React yet. The Configure Role route lives in React, but only the
// Credentials tab has been migrated so far; everything else still has its
// CLJS implementation and can be reached via the legacy connection editor.
//
// This intentionally points the user back at the legacy form rather than
// rendering an empty page. Replace this with the real tab once it's
// migrated.
export default function PlaceholderTab({ tabName, connectionName }) {
  return (
    <Stack gap="md" maw={720}>
      <Alert variant="light" color="yellow" icon={<Info size={16} />}>
        <Stack gap={6}>
          <Text size="sm" fw={600}>
            {tabName + ' is not migrated to the new editor yet.'}
          </Text>
          <Text size="sm">
            The Credentials tab here uses the new write-only flow. For
            everything else, continue using the legacy editor for now.
          </Text>
          <Button
            variant="default"
            size="xs"
            w="fit-content"
            rightSection={<ExternalLink size={14} />}
            component="a"
            href={'/connections/edit/' + encodeURIComponent(connectionName)}
          >
            Open legacy editor
          </Button>
        </Stack>
      </Alert>
    </Stack>
  )
}

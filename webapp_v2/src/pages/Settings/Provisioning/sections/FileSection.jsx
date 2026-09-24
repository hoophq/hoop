import { Stack, Text } from '@mantine/core'
import SectionRow from '@/components/SectionRow'
import UserImportForm from '@/features/UserImport'

/**
 * File import: an admin uploads users and their groups, for an org without the
 * Slack import. It is refused while the Slack import manages the groups.
 */
export default function FileSection({ groupsManaged }) {
  return (
    <SectionRow
      title="Import a file"
      description="The simplest source: a CSV of users and their groups. Import it again to change them, or edit groups on the Users page."
    >
      <Stack gap="md">
        {groupsManaged && (
          <Text size="sm" c="red">
            The Slack import manages the groups. Stop managing them below before importing a file.
          </Text>
        )}
        <UserImportForm disabled={groupsManaged} />
      </Stack>
    </SectionRow>
  )
}

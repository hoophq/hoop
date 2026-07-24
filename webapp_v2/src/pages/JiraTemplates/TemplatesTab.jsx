import { Fragment, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Card, Divider, Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import EmptyState from '@/layout/EmptyState'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { useUserStore } from '@/stores/useUserStore'
import { useJiraTemplatesStore } from './store'
import TemplateListItem from './sections/TemplateListItem'

const FREE_LICENSE_INFO_MESSAGE =
  'Organizations with Free plan have limited automation. Upgrade to Enterprise to have unlimited access to Jira Templates.'
const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Jira Templates free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

export default function TemplatesTab() {
  const navigate = useNavigate()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  const list = useJiraTemplatesStore((s) => s.list)
  const submitting = useJiraTemplatesStore((s) => s.submitting)
  const deleteTemplate = useJiraTemplatesStore((s) => s.deleteTemplate)

  const [deleteOpened, deleteModal] = useDisclosure(false)
  const [templateToDelete, setTemplateToDelete] = useState(null)

  const goCreate = () => navigate('/jira-templates/new')

  const openDelete = (template) => {
    setTemplateToDelete(template)
    deleteModal.open()
  }

  const handleDelete = async () => {
    const { ok, error } = await deleteTemplate(templateToDelete.id)
    deleteModal.close()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Jira template deleted.' })
    } else {
      showSnackbar({
        level: 'error',
        text: error?.response?.data?.message || 'Failed to delete Jira template.',
      })
    }
  }

  if (list.length === 0) {
    return (
      <Stack gap="xl">
        {isFreeLicense && (
          <FreeLicenseCallout message={FREE_LICENSE_INFO_MESSAGE} variant="info" />
        )}
        <EmptyState
          title="No Jira template configured in your Organization yet."
          action={{ label: 'Create a new JIRA Template', onClick: goCreate }}
          docsUrl={docsUrl.integrations.jira}
          docsLabel="Jira templates documentation"
        />
      </Stack>
    )
  }

  const atFreeLimit = isFreeLicense && list.length >= 1

  return (
    <Stack gap="xl">
      {atFreeLimit && (
        <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />
      )}

      <Card padding={0} withBorder radius="md">
        {list.map((template, idx) => (
          <Fragment key={template.id}>
            {idx > 0 && <Divider />}
            <Box bg="white">
              <TemplateListItem
                template={template}
                onConfigure={(id) => navigate(`/jira-templates/edit/${id}`)}
                onDelete={openDelete}
              />
            </Box>
          </Fragment>
        ))}
      </Card>

      <Modal
        opened={deleteOpened}
        onClose={deleteModal.close}
        title="Delete Jira template?"
      >
        <Stack gap="lg">
          <Text size="sm">
            {`This action will permanently delete the '${templateToDelete?.name ?? ''}' template and cannot be undone. Are you sure you want to proceed?`}
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={deleteModal.close}>
              Cancel
            </Button>
            <Button color="red" onClick={handleDelete} loading={submitting}>
              Delete
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}

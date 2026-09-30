import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useAiSessionAnalyzerStore } from '../store'
import SidecarAiAnalyzerFields from './SidecarAiAnalyzerFields'
import { useSidecarAiAnalyzerEditor } from './useSidecarAiAnalyzerEditor'
import classes from './Create.module.css'

// The sidecar's sibling of Create/GatewayAiAnalyzerForm. Neither imports the
// other; Router.jsx picks one with <ByProduct>. The form itself is
// SidecarAiAnalyzerFields, which the sidecar pages also open in a dialog;
// this file is the page around it.

function FormFields({ rule: stored, ruleName, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)
  const back = () => navigate('/features/ai-session-analyzer')
  const editor = useSidecarAiAnalyzerEditor({ rule: stored, ruleName, isEdit, onSaved: back, onDeleted: back })

  const handleDelete = async () => {
    await editor.remove()
    deleteModal.close()
  }

  return (
    <Stack gap={0}>
      <Box>
        <Button
          variant="transparent"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={back}
          px={0}
          w="fit-content"
          mb="xl"
        >
          Back
        </Button>
      </Box>

      {/* The header of the gateway sibling: pulled up by the shell header's
          height, since useInViewport takes no rootMargin. */}
      <Box
        ref={sentinelRef}
        aria-hidden="true"
        pos="relative"
        top="calc(-1 * var(--app-shell-header-offset, 0rem))"
      />
      <Group
        justify="space-between"
        align="center"
        pos="sticky"
        top="var(--app-shell-header-offset, 0rem)"
        bg="var(--mantine-color-body)"
        py="md"
        mb="xl"
        mx={-PAGE_PADDING}
        px={PAGE_PADDING}
        className={classes.stickyHeader}
        data-scrolled={!headerInView || undefined}
      >
        <Title order={2} lts="-0.00625em">
          {isEdit ? 'Edit AI Analyzer rule' : 'Create new AI Analyzer rule'}
        </Title>
        <Group gap="sm">
          {isEdit && (
            <Button variant="subtle" color="red" onClick={deleteModal.open} disabled={editor.submitting}>
              Delete
            </Button>
          )}
          <Button onClick={editor.save} disabled={!editor.canSubmit} loading={editor.submitting}>
            Save
          </Button>
        </Group>
      </Group>

      <SidecarAiAnalyzerFields editor={editor} />

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete rule">
        <Stack gap="lg">
          <Text size="sm">
            {`Are you sure you want to delete the rule "${ruleName}"? Every listener it is distributed to stops running it. This action cannot be undone.`}
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={deleteModal.close}>
              Cancel
            </Button>
            <Button color="red" onClick={handleDelete} loading={editor.submitting}>
              Delete
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}

export default function ControlPlaneAiAnalyzerForm() {
  const { ruleName } = useParams()
  const isEdit = Boolean(ruleName)

  const active = useAiSessionAnalyzerStore((s) => s.active)
  const activeStatus = useAiSessionAnalyzerStore((s) => s.activeStatus)
  const fetchActive = useAiSessionAnalyzerStore((s) => s.fetchActive)
  const clearActive = useAiSessionAnalyzerStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(ruleName)
    return () => clearActive()
  }, [isEdit, ruleName, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  if (isEdit && activeStatus === 'error') {
    return <PageLoader error h={400} message="Failed to load rule." />
  }

  return (
    <FormFields
      key={isEdit ? (active?.name ?? ruleName) : 'new'}
      rule={isEdit ? active : null}
      ruleName={ruleName}
      isEdit={isEdit}
    />
  )
}

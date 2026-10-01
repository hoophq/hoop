import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useGuardrailsStore } from '../store'
import SidecarGuardrailFields from './SidecarGuardrailFields'
import { useSidecarGuardrailEditor } from './useSidecarGuardrailEditor'
import classes from './Create.module.css'

// Page shell around SidecarGuardrailFields; Router.jsx picks this or the Gateway sibling with <ByProduct>.

function FormFields({ guardrail, id, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)
  const back = () => navigate('/guardrails')
  const editor = useSidecarGuardrailEditor({ guardrail, id, isEdit, onSaved: back, onDeleted: back })

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
          {isEdit ? 'Configure Guardrail' : 'Create a new Guardrail'}
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

      <SidecarGuardrailFields editor={editor} />

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Guardrail?">
        <Stack gap="lg">
          <Text size="sm">
            This action will permanently delete this Guardrail and cannot be undone. Every
            listener it is distributed to stops enforcing it. Are you sure you want to
            proceed?
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

export default function ControlPlaneGuardrailForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)

  const active = useGuardrailsStore((s) => s.active)
  const activeStatus = useGuardrailsStore((s) => s.activeStatus)
  const fetchActive = useGuardrailsStore((s) => s.fetchActive)
  const clearActive = useGuardrailsStore((s) => s.clearActive)

  useEffect(() => {
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  // A failed fetch leaves `active` null; rendering the form would present a
  // blank "edit" whose save overwrites the real guardrail with empty rules.
  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load guardrail.</Text>
  }

  return (
    <FormFields
      key={isEdit ? (active?.id ?? id) : 'new'}
      guardrail={isEdit ? active : null}
      id={id}
      isEdit={isEdit}
    />
  )
}

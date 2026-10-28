import { useEffect } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure, useInViewport } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import EnterpriseBanner from '@/components/EnterpriseBanner'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { PAGE_PADDING } from '@/layout/PageLayout'
import { useUserStore } from '@/stores/useUserStore'
import { useDataMaskingStore } from '../store'
import { clampRuleToFreePlan, findMaskingTemplate } from '../templates'
import DataMaskingFields from './DataMaskingFields'
import { useDataMaskingEditor } from './useDataMaskingEditor'
import classes from './Create.module.css'

// Page shell around DataMaskingFields; the sidecar dialog shares the editor and fields.

function FormFields({ rule: stored, id, isEdit }) {
  const navigate = useNavigate()
  const { ref: sentinelRef, inViewport: headerInView } = useInViewport()
  const [deleteOpened, deleteModal] = useDisclosure(false)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const back = () => navigate('/features/data-masking')
  const editor = useDataMaskingEditor({ rule: stored, id, isEdit, onSaved: back, onDeleted: back })

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

      {/* Pulled up by the shell header's height so it leaves the viewport when
          the sticky bar meets the header: useInViewport takes no rootMargin. */}
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
          {isEdit ? 'Edit Data Masking rule' : 'Create new Data Masking rule'}
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

      {isFreeLicense && (
        <Box mb="xl">
          <EnterpriseBanner />
        </Box>
      )}

      <DataMaskingFields editor={editor} />

      <Modal opened={deleteOpened} onClose={deleteModal.close} title="Delete Data Masking rule?">
        <Stack gap="lg">
          <Text size="sm">
            This action will permanently delete this Data Masking rule and cannot be undone.
            Every resource role and listener it applies to stops masking with it. Are you
            sure you want to proceed?
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

export default function DataMaskingForm() {
  const { id } = useParams()
  const isEdit = Boolean(id)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  // Activation-journey deep link: /features/data-masking/new?template=<id>&connections=<ids>
  // seeds a recommended rule. An unknown id falls back to the blank form; the URL
  // is the source of truth, so a refresh re-seeds the same template. The free
  // plan clamps the template to its one-entity-per-rule limit.
  const [searchParams] = useSearchParams()
  const template = isEdit ? null : findMaskingTemplate(searchParams.get('template'))
  const templateConnectionIds = (searchParams.get('connections') ?? '').split(',').filter(Boolean)
  const seededRule = template ? { ...template.rule, connection_ids: templateConnectionIds } : null
  const templateRule = seededRule && isFreeLicense ? clampRuleToFreePlan(seededRule) : seededRule

  const active = useDataMaskingStore((s) => s.active)
  const activeStatus = useDataMaskingStore((s) => s.activeStatus)
  const fetchActive = useDataMaskingStore((s) => s.fetchActive)
  const clearActive = useDataMaskingStore((s) => s.clearActive)
  const fetchAttributes = useDataMaskingStore((s) => s.fetchAttributes)

  useEffect(() => {
    fetchAttributes()
    if (isEdit) fetchActive(id)
    return () => clearActive()
  }, [isEdit, id, fetchAttributes, fetchActive, clearActive])

  if (isEdit && (activeStatus === 'loading' || activeStatus === 'idle')) {
    return <PageLoader h={400} />
  }
  // A failed fetch leaves `active` null; rendering the form would present a
  // blank "edit" whose save overwrites the real rule with defaults.
  if (isEdit && activeStatus === 'error') {
    return <Text c="red">Failed to load data masking rule.</Text>
  }

  return (
    <FormFields
      key={isEdit ? (active?.id ?? id) : template ? `template-${template.id}` : 'new'}
      rule={isEdit ? active : templateRule}
      id={id}
      isEdit={isEdit}
    />
  )
}

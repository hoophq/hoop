import { Group, Loader, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import { GATE_STATUS, useJiraPromptStore } from '@/stores/useJiraPromptStore'
import PromptForm from './PromptForm'

const TITLES = {
  [GATE_STATUS.READY]: 'Required information',
  [GATE_STATUS.ERROR]: 'Jira CMDB Connection Issue',
  [GATE_STATUS.LOADING]: 'Loading Jira Template',
}

function CmdbError({ onRetry, onCancel }) {
  return (
    <Stack gap="lg">
      <Stack gap="sm">
        <Text>
          {"We couldn't retrieve some required CMDB data from your Jira server. This template requires valid CMDB fields to continue."}
        </Text>
        {/* amber, not orange: theme.js has no orange palette. */}
        <Text fz="sm" c="amber.9">
          This is usually due to Jira server availability or permission issues. Please contact your
          Jira administrator to ensure the CMDB service is functioning properly.
        </Text>
      </Stack>
      <Group justify="flex-end" gap="sm">
        <Button variant="default" onClick={onCancel}>
          Cancel
        </Button>
        <Button onClick={onRetry}>Try again</Button>
      </Group>
    </Stack>
  )
}

function Loading() {
  return (
    <Stack gap="lg">
      <Text>
        Loading template data from Jira. If your template includes CMDB fields, this may take longer
        as we need to fetch additional data from your Jira server.
      </Text>
      <Group justify="flex-end">
        <Loader size="sm" />
      </Group>
    </Stack>
  )
}

/**
 * The Jira prompt gate: the template's prompts and CMDB fields, as a modal.
 * Open it only when useJiraPromptStore's load() answered `needed`.
 * onSubmit gets the request body part ({ jira_fields }); Cancel, Escape and
 * a click outside call onCancel and send nothing.
 */
export default function JiraPromptGate({ opened, onSubmit, onCancel, zIndex }) {
  const status = useJiraPromptStore((s) => s.status)
  const retry = useJiraPromptStore((s) => s.retry)
  const abandon = useJiraPromptStore((s) => s.abandon)

  // Every way out (Cancel, the X, Escape, a click outside) drops what is
  // still in flight, so nothing is sent after it.
  const cancel = () => {
    abandon()
    onCancel()
  }

  // A retry that finds every preset and no prompt goes on without the form.
  // A cancelled retry resolves to null.
  const handleRetry = async () => {
    const result = await retry()
    if (result && !result.needed) onSubmit(result.fields)
  }

  return (
    <Modal
      opened={opened}
      onClose={cancel}
      title={TITLES[status]}
      size="lg"
      closeOnClickOutside={status !== GATE_STATUS.LOADING}
      zIndex={zIndex}
    >
      {status === GATE_STATUS.READY && <PromptForm onSubmit={onSubmit} onCancel={cancel} />}
      {status === GATE_STATUS.ERROR && <CmdbError onRetry={handleRetry} onCancel={cancel} />}
      {status === GATE_STATUS.LOADING && <Loading />}
    </Modal>
  )
}

import { useEffect, useMemo, useState } from 'react'
import { Group, ScrollArea, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import SidecarAiAnalyzerFields from '@/pages/Features/AiSessionAnalyzer/Create/SidecarAiAnalyzerFields'
import { useSidecarAiAnalyzerEditor } from '@/pages/Features/AiSessionAnalyzer/Create/useSidecarAiAnalyzerEditor'
import SidecarDataMaskingFields from '@/pages/Features/DataMasking/Create/SidecarDataMaskingFields'
import { useSidecarDataMaskingEditor } from '@/pages/Features/DataMasking/Create/useSidecarDataMaskingEditor'
import SidecarGuardrailFields from '@/pages/Guardrails/Create/SidecarGuardrailFields'
import { useSidecarGuardrailEditor } from '@/pages/Guardrails/Create/useSidecarGuardrailEditor'
import { RULE_APIS, lanesAsTargets, ruleErrorMessage, targetLanes } from '../rules'

function Footer({ editor, onCancel }) {
  return (
    <Group justify="flex-end" gap="sm">
      <Button variant="default" onClick={onCancel} disabled={editor.submitting}>
        Cancel
      </Button>
      <Button onClick={editor.save} loading={editor.submitting} disabled={!editor.canSubmit}>
        Save
      </Button>
    </Group>
  )
}

// One body per feature, so the hook never changes under a mounted dialog.
function GuardrailBody({ stored, targets, onSaved, onCancel }) {
  const editor = useSidecarGuardrailEditor({
    guardrail: stored,
    id: stored?.id,
    isEdit: Boolean(stored),
    targets,
    onSaved,
  })
  return (
    <Stack gap="xl">
      <SidecarGuardrailFields editor={editor} />
      <Footer editor={editor} onCancel={onCancel} />
    </Stack>
  )
}

function DataMaskingBody({ stored, targets, onSaved, onCancel }) {
  const editor = useSidecarDataMaskingEditor({
    rule: stored,
    id: stored?.id,
    isEdit: Boolean(stored),
    targets,
    onSaved,
  })
  return (
    <Stack gap="xl">
      <SidecarDataMaskingFields editor={editor} />
      <Footer editor={editor} onCancel={onCancel} />
    </Stack>
  )
}

function AnalyzerBody({ stored, targets, onSaved, onCancel }) {
  const editor = useSidecarAiAnalyzerEditor({
    rule: stored,
    ruleName: stored?.name,
    isEdit: Boolean(stored),
    targets,
    onSaved,
  })
  return (
    <Stack gap="xl">
      <SidecarAiAnalyzerFields editor={editor} />
      <Footer editor={editor} onCancel={onCancel} />
    </Stack>
  )
}

const BODIES = { guardrails: GuardrailBody, 'data-masking': DataMaskingBody, 'ai-analyzer': AnalyzerBody }

// A binding only names the rule, so an edit finds the record in the list first.
function Form({ feature, sidecar, listener, ruleName, onClose, onSaved }) {
  const api = RULE_APIS[feature.key]
  const [stored, setStored] = useState(null)
  const [status, setStatus] = useState(ruleName ? 'loading' : 'ready')
  const [error, setError] = useState(null)

  useEffect(() => {
    if (!ruleName) return
    let cancelled = false
    const load = async () => {
      const found = (await api.list()).find((r) => r.name === ruleName)
      if (!found) throw new Error(`Rule "${ruleName}" was not found.`)
      const { data } = await api.get(found)
      if (cancelled) return
      setStored(data)
      setStatus('ready')
    }
    load().catch((err) => {
      if (cancelled) return
      setError(ruleErrorMessage(err))
      setStatus('error')
    })
    return () => {
      cancelled = true
    }
  }, [api, ruleName])

  const targets = useMemo(() => lanesAsTargets(sidecar, targetLanes(sidecar, listener)), [sidecar, listener])
  const Body = BODIES[feature.key]

  if (status === 'loading') return <PageLoader h={300} />
  if (status === 'error') {
    return (
      <Text size="sm" c="red">
        {error}
      </Text>
    )
  }
  return <Body stored={stored} targets={stored ? [] : targets} onSaved={onSaved} onCancel={onClose} />
}

export default function RuleFormModal({ opened, feature, sidecar, listener, rule, onClose, onSaved }) {
  if (!feature) return null
  const ruleName = rule?.name ?? null
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size={892}
      title={ruleName ? `Edit ${ruleName}` : `New ${feature.label} rule`}
      closeOnClickOutside={false}
      scrollAreaComponent={ScrollArea.Autosize}
    >
      <Form
        feature={feature}
        sidecar={sidecar}
        listener={listener}
        ruleName={ruleName}
        onClose={onClose}
        onSaved={onSaved}
      />
    </Modal>
  )
}

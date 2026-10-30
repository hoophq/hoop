import { useState } from 'react'
import Tabs from '@/components/Tabs'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { HIDDEN_METADATA_KEYS, isOutputHidden } from '../sessionState'
import InputTab from '../tabs/InputTab'
import OutputTab from '../tabs/OutputTab'
import AccessRequestTab from '../tabs/AccessRequestTab'
import MetadataTab from '../tabs/MetadataTab'
import RunbookTab from '../tabs/RunbookTab'

// Figma "Tabs Module": a tab renders only when its data exists, in the order
// Input, Output, Access Request, Metadata, Runbook. Output opens first.
export default function DetailsTabs({ session }) {
  const largeInput = useSessionDetailsStore((s) => s.largeInput)
  const execution = useSessionDetailsStore((s) => s.executions[session.id])
  const metadata = session.metadata || {}

  const tabs = [
    (session.script?.data || largeInput) && { value: 'input', label: 'Input', Panel: InputTab },
    !isOutputHidden(session, execution) && { value: 'output', label: 'Output', Panel: OutputTab },
    session.review?.review_groups_data?.length > 0 && {
      value: 'access-request',
      label: 'Access Request',
      Panel: AccessRequestTab,
    },
    (metadata.sidecar ||
      metadata.sidecar_session_id ||
      Object.keys(metadata).some((key) => !HIDDEN_METADATA_KEYS.has(key))) && {
      value: 'metadata',
      label: 'Metadata',
      Panel: MetadataTab,
    },
    session.labels?.runbookFile && { value: 'runbook', label: 'Runbook', Panel: RunbookTab },
  ].filter(Boolean)

  const hasOutput = tabs.some((t) => t.value === 'output')
  const initial = hasOutput ? 'output' : tabs[0]?.value
  const [active, setActive] = useState(initial)
  const value = tabs.some((t) => t.value === active) ? active : initial

  // The output appears after an Execute (or when a running exec ends): show it.
  const [hadOutput, setHadOutput] = useState(hasOutput)
  if (hasOutput !== hadOutput) {
    setHadOutput(hasOutput)
    if (hasOutput) setActive('output')
  }

  if (!tabs.length) return null

  return (
    <Tabs value={value} onChange={setActive} keepMounted={false}>
      <Tabs.List>
        {tabs.map((tab) => (
          <Tabs.Tab key={tab.value} value={tab.value}>
            {tab.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {tabs.map(({ value: tab, Panel }) => (
        <Tabs.Panel key={tab} value={tab} pt="lgAlt">
          <Panel session={session} />
        </Tabs.Panel>
      ))}
    </Tabs>
  )
}

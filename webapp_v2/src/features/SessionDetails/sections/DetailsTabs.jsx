import { useState } from 'react'
import { Stack } from '@mantine/core'
import Tabs from '@/components/Tabs'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { HIDDEN_METADATA_KEYS, blockReasonTab, isOutputHidden } from '../sessionState'
import Insights from '../insights/Insights'
import InputTab from '../tabs/InputTab'
import OutputTab from '../tabs/OutputTab'
import AccessRequestTab from '../tabs/AccessRequestTab'
import MetadataTab from '../tabs/MetadataTab'
import RunbookTab from '../tabs/RunbookTab'

// Figma "Tabs Module": a tab renders only when its data exists, in the order
// Input, Output, Access Request, Metadata, Runbook. Output opens first, except
// for a blocked session: the tab with the reason does. The insight cards of a
// tab that does not render stay above the tabs.
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

  const has = (value) => tabs.some((t) => t.value === value)
  const hasOutput = has('output')
  const reasonTab = blockReasonTab(session)
  const initial = reasonTab && has(reasonTab) ? reasonTab : hasOutput ? 'output' : tabs[0]?.value
  const tabless = ['input', 'output'].filter((value) => !has(value))
  const [active, setActive] = useState(initial)
  const value = tabs.some((t) => t.value === active) ? active : initial

  // The output appears after an Execute (or when a running exec ends): show it.
  const [hadOutput, setHadOutput] = useState(hasOutput)
  if (hasOutput !== hadOutput) {
    setHadOutput(hasOutput)
    if (hasOutput) setActive('output')
  }

  const orphans = tabless.length > 0 && <Insights session={session} tabs={tabless} />
  if (!tabs.length) return orphans || null

  return (
    <Stack gap="lgAlt">
      {orphans}
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
    </Stack>
  )
}

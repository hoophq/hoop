import { Stack } from '@mantine/core'
import { insightsByTab } from '../sessionState'
import AiAnalysis from './AiAnalysis'
import Guardrails from './Guardrails'

// What judged the session before or while it ran, for the given tabs: the AI
// Session Analyzer and the input guardrails sit in the Input tab, the output
// guardrails in the Output tab (NB19). Nothing renders when none did.
export default function Insights({ session, tabs }) {
  const byTab = insightsByTab(session)
  const analysis = tabs.includes('input') ? byTab.input.analysis : null
  const guardrails = tabs.flatMap((tab) => byTab[tab]?.guardrails || [])
  if (!analysis && !guardrails.length) return null
  return (
    <Stack gap="sm">
      {analysis && <AiAnalysis analysis={analysis} />}
      {guardrails.length > 0 && <Guardrails entries={guardrails} />}
    </Stack>
  )
}

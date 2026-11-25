import { Stack } from '@mantine/core'
import { insightsByTab } from '../sessionState'
import AiAnalysis from './AiAnalysis'
import AiDecisions from './AiDecisions'
import Guardrails from './Guardrails'
import { sidecarDecisions } from './helpers'

// What judged the session before or while it ran, for the given tabs: the AI
// Session Analyzer, the sidecar analyzer's decisions and the input guardrails
// sit in the Input tab, the output guardrails in the Output tab (NB19).
// Nothing renders when none did.
export default function Insights({ session, tabs }) {
  const byTab = insightsByTab(session)
  const input = tabs.includes('input')
  const analysis = input ? byTab.input.analysis : null
  const sidecar = input ? sidecarDecisions(session) : { decisions: [], dropped: 0 }
  const guardrails = tabs.flatMap((tab) => byTab[tab]?.guardrails || [])
  if (!analysis && !sidecar.decisions.length && !guardrails.length) return null
  return (
    <Stack gap="sm">
      {analysis && <AiAnalysis analysis={analysis} />}
      {sidecar.decisions.length > 0 && <AiDecisions decisions={sidecar.decisions} dropped={sidecar.dropped} />}
      {guardrails.length > 0 && <Guardrails entries={guardrails} />}
    </Stack>
  )
}

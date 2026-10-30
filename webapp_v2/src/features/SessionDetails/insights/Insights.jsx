import { Stack } from '@mantine/core'
import AiAnalysis from './AiAnalysis'
import Guardrails from './Guardrails'

// What judged the session before or while it ran: the AI Session Analyzer and
// the guardrails. Nothing renders when neither did.
export default function Insights({ session }) {
  const analysis = session.ai_analysis
  const guardrails = session.guardrails_info || []
  const hasAnalysis = analysis && Object.keys(analysis).length > 0
  if (!hasAnalysis && !guardrails.length) return null
  return (
    <Stack gap="sm">
      {hasAnalysis && <AiAnalysis analysis={analysis} />}
      {guardrails.length > 0 && <Guardrails entries={guardrails} />}
    </Stack>
  )
}

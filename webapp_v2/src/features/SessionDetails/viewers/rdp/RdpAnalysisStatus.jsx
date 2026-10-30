import AnalysisStatus from './AnalysisStatus'
import { useRdpAnalysis } from './useRdpAnalysis'

// The RDP PII analysis status in the title row of the data masking block, so
// it shows without opening the player. Only RDP recordings are analyzed.
export default function RdpAnalysisStatus({ session }) {
  const rdp = session?.connection_subtype === 'rdp'
  const { analysis, retrying, retry } = useRdpAnalysis(rdp ? session.id : null)
  if (!rdp) return null
  return <AnalysisStatus analysis={analysis} retrying={retrying} onRetry={retry} />
}

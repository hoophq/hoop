import { Anchor, Box, Group, Stack, Text } from '@mantine/core'
import { ArrowRightLeft, ArrowUpRight, LayoutList, Sparkles } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import RdpAnalysisStatus from '../viewers/rdp/RdpAnalysisStatus'
import InsightCard from './InsightCard'
import { categoriesText, itemsText, maskingAnalytics } from './helpers'
import classes from './insights.module.css'

const DATA_MASKING_PATH = '/features/data-masking'

// Closes the session details first: the page opens behind the modal otherwise.
function ConfigureLink({ size = 'sm' }) {
  const navigate = useNavigate()
  const close = useSessionDetailsStore((s) => s.close)
  return (
    <Anchor
      component="button"
      size={size}
      fw={500}
      onClick={(event) => {
        event.stopPropagation()
        close()
        navigate(DATA_MASKING_PATH)
      }}
    >
      <Group gap={4} component="span" wrap="nowrap">
        Configure it on Live Data Masking
        <ArrowUpRight size={14} />
      </Group>
    </Anchor>
  )
}

// Matrix I08: masking is not configured and the session holds no masking data.
function UnlockCallout() {
  return (
    <Box className={classes.card} data-variant="violet">
      <Group gap="sm" align="flex-start" wrap="nowrap">
        <Sparkles size={16} color="var(--mantine-color-violet-5)" aria-hidden />
        <Stack gap="xs">
          <Text size="sm" fw={700}>
            Unlock Live Data Masking
          </Text>
          <Text size="sm">
            Redact sensitive fields on the fly to reduce exposure risk and keep your data pipelines compliant.
          </Text>
          <ConfigureLink />
          <Anchor href={docsUrl.features.aiDatamasking} target="_blank" rel="noopener noreferrer" size="sm" fw={500}>
            <Group gap={4} component="span" wrap="nowrap">
              Go to Live Data Masking Docs
              <ArrowUpRight size={14} />
            </Group>
          </Anchor>
        </Stack>
      </Group>
    </Box>
  )
}

function Tile({ icon: Icon, label, value }) {
  return (
    <Stack gap={4} align="center" className={classes.tile}>
      <Icon size={16} color="var(--mantine-color-violet-5)" aria-hidden />
      <Text size="sm" ta="center">
        {label}
      </Text>
      <Text size="sm" fw={500} ta="center">
        {value}
      </Text>
    </Stack>
  )
}

function Summary({ label, value }) {
  return (
    <Text size="sm" fw={500} span>
      {`${label}: `}
      <Text span size="sm" fw={400}>
        {value}
      </Text>
    </Text>
  )
}

// Figma "Output Tab | AI Data Masking" (matrix I09–I11): what the masking
// redacted, with the RDP PII analysis status for an RDP session.
export default function DataMasking({ session }) {
  const report = useSessionDetailsStore((s) => s.report)
  const redactProvider = useUserStore((s) => s.redactProvider)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const { items, total } = maskingAnalytics(report, session)
  const hasData = items.length > 0 || total > 0
  // A sidecar masks on its own, whatever the gateway's provider and license.
  const sidecar = session.identity_type === 'sidecar'

  if (!hasData && redactProvider !== 'mspresidio') return <UnlockCallout />

  const promote = isFreeLicense && !sidecar
  const title = (
    <Stack gap={4} align="flex-start">
      <Group gap="xs" wrap="nowrap">
        <Sparkles size={16} color="var(--mantine-color-violet-5)" aria-hidden />
        <Text size="sm" fw={700}>
          {promote ? 'Enable Live Data Masking' : 'Live Data Masking'}
        </Text>
      </Group>
      {promote && (
        <Text size="sm" ta="left">
          We detected sensitive data that could be protected with automated data masking.
        </Text>
      )}
    </Stack>
  )

  return (
    <InsightCard
      variant="violet"
      title={title}
      summary={
        !promote && (
          <Group gap="md" wrap="nowrap">
            <Summary label="Data Categories" value={categoriesText(items, { full: false })} />
            <Summary label="Volume of Data" value={itemsText(total)} />
          </Group>
        )
      }
      aside={
        <>
          {promote && <ConfigureLink />}
          {session.connection_subtype === 'rdp' && <RdpAnalysisStatus session={session} />}
        </>
      }
    >
      <Group gap="sm" wrap="nowrap" align="stretch">
        <Tile icon={LayoutList} label="Data Categories" value={categoriesText(items, { full: true })} />
        <Tile icon={ArrowRightLeft} label="Volume of Data" value={itemsText(total)} />
      </Group>
    </InsightCard>
  )
}

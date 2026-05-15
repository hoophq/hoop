import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack, Tabs, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import PageLoader from '@/components/PageLoader'
import { useRulepackStore } from '../store'
import FeatureFlagGate from '../FeatureFlagGate'
import ConfigurationTab from './ConfigurationTab'
import RolesTab from './RolesTab'

function DetailHeader({ rulepack, onBack }) {
  return (
    <Stack gap="md">
      <Group
        gap={4}
        align="center"
        c="dimmed"
        onClick={onBack}
        style={{ cursor: 'pointer', width: 'fit-content' }}
      >
        <ArrowLeft size={16} />
        <Text size="sm">Rulepacks</Text>
      </Group>
      <Title order={1}>{rulepack.display_name || ''}</Title>
      {rulepack.description && (
        <Text size="md" c="dimmed">
          {rulepack.description}
        </Text>
      )}
    </Stack>
  )
}

function RulepackDetailContent() {
  const navigate = useNavigate()
  const { id } = useParams()
  const { active, activeStatus, selectedConnections, fetchActive } = useRulepackStore()

  useEffect(() => {
    if (id) fetchActive(id)
  }, [id, fetchActive])

  const loading = activeStatus === 'loading' || !active
  if (loading) {
    return <PageLoader h={400} />
  }

  const rules =
    (active.data_masking_rules?.length ?? 0) +
    (active.guardrail_rules?.length ?? 0)
  const selectedCount = selectedConnections.size

  return (
    <Stack gap="xl">
      <DetailHeader rulepack={active} onBack={() => navigate('/rulepacks')} />

      <Tabs defaultValue="roles" keepMounted={false}>
        <Tabs.List>
          <Tabs.Tab value="roles" pb="sm">
            {`Roles • ${selectedCount} selected`}
          </Tabs.Tab>
          <Tabs.Tab value="configuration" pb="sm">
            {`Configuration • ${rules} ${rules === 1 ? 'rule' : 'rules'}`}
          </Tabs.Tab>
        </Tabs.List>

        <Box pt="lg">
          <Tabs.Panel value="roles">
            <RolesTab />
          </Tabs.Panel>
          <Tabs.Panel value="configuration">
            <ConfigurationTab rulepack={active} />
          </Tabs.Panel>
        </Box>
      </Tabs>
    </Stack>
  )
}

export default function RulepackDetail() {
  const { featureFlags } = useUserStore()
  return (
    <FeatureFlagGate enabled={!!featureFlags['experimental.rulepacks']}>
      <RulepackDetailContent />
    </FeatureFlagGate>
  )
}

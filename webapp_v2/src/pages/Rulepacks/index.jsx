import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Flex, Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import { ArrowRight, EyeOff, Search, Shield, Sparkles } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import Badge from '@/components/Badge'
import List from '@/components/List'
import EmptyState from '@/layout/EmptyState'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { useRulepackStore } from './store'
import FeatureFlagGate from './FeatureFlagGate'

const FREE_LICENSE_MESSAGE =
  'Applying rulepacks to Resource Roles is an Enterprise feature. Upgrade to roll out Guardrails and Data Masking rules across many Resource Roles at once.'

const SEARCH_DEBOUNCE_MS = 300

function FeatureBadge({ icon, label }) {
  const Icon = icon
  return (
    <Badge variant="light" color="gray" leftSection={<Icon size={12} />}>
      {label}
    </Badge>
  )
}

function RulepackRow({ rulepack, onConfigure }) {
  const hasDataMasking = (rulepack.data_masking_rules ?? []).length > 0
  const hasGuardrails = (rulepack.guardrail_rules ?? []).length > 0
  const hasAiAnalyzer = (rulepack.ai_session_analyzer_rules ?? []).length > 0
  const anyBadge = hasDataMasking || hasGuardrails || hasAiAnalyzer

  return (
    <Box p="lg">
      <Flex align="center" gap="md">
        <Stack gap={4} flex={1} miw={0}>
          <Text size="lg" fw={700} lineClamp={1}>
            {rulepack.display_name}
          </Text>
          {rulepack.description && (
            <Text size="sm" c="dimmed">
              {rulepack.description}
            </Text>
          )}
          {anyBadge && (
            <Group gap="xs" pt={4}>
              {hasDataMasking && <FeatureBadge icon={EyeOff} label="Data Masking" />}
              {hasGuardrails && <FeatureBadge icon={Shield} label="Guardrails" />}
              {hasAiAnalyzer && (
                <FeatureBadge icon={Sparkles} label="AI Analyzer" />
              )}
            </Group>
          )}
        </Stack>
        <Group gap="xs" wrap="nowrap">
          <Button
            variant="default"
            size="sm"
            onClick={() => onConfigure(rulepack.id)}
            rightSection={<ArrowRight size={16} />}
          >
            Configure
          </Button>
        </Group>
      </Flex>
    </Box>
  )
}

function RulepackListContent() {
  const navigate = useNavigate()
  const { list, listStatus, listSearch, fetchList } = useRulepackStore()
  const isFreeLicense = useUserStore((state) => state.isFreeLicense)

  const [input, setInput] = useState(listSearch ?? '')
  const debounceRef = useRef(null)

  useEffect(() => {
    fetchList()
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [fetchList])

  const handleSearchChange = (event) => {
    const value = event.currentTarget.value
    setInput(value)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      debounceRef.current = null
      fetchList(value)
    }, SEARCH_DEBOUNCE_MS)
  }

  const loading = listStatus === 'loading'
  const showLoader = loading && list.length === 0
  const searching = useMemo(
    () => input.trim().length > 0 || listSearch.length > 0,
    [input, listSearch],
  )

  return (
    <Stack gap="xl">
      <Stack gap="sm">
        <Title order={1}>Rulepacks</Title>
        <Text size="md" c="dimmed">
          Bundle rules and apply them to many Resource Roles at once.
        </Text>
      </Stack>

      {isFreeLicense && <FreeLicenseCallout message={FREE_LICENSE_MESSAGE} />}

      <TextInput
        placeholder="Search rulepacks by name"
        value={input}
        onChange={handleSearchChange}
        leftSection={<Search size={16} />}
      />

      {showLoader ? (
        <PageLoader h={300} />
      ) : listStatus === 'error' ? (
        <PageLoader error h={300} message="Failed to load rulepacks." />
      ) : list.length === 0 ? (
        <EmptyState
          compact
          title={searching ? 'No rulepacks match your search' : 'No rulepacks yet'}
          description={
            searching
              ? 'Try a different search term.'
              : 'Rulepacks bundle related rules so you can apply them to many Resource Roles at once.'
          }
        />
      ) : (
        <List>
          {list.map((rulepack) => (
            <RulepackRow
              key={rulepack.id}
              rulepack={rulepack}
              onConfigure={(id) => navigate(`/rulepacks/${id}`)}
            />
          ))}
        </List>
      )}
    </Stack>
  )
}

export default function Rulepacks() {
  const { featureFlags } = useUserStore()
  return (
    <FeatureFlagGate enabled={!!featureFlags['experimental.rulepacks']}>
      <RulepackListContent />
    </FeatureFlagGate>
  )
}

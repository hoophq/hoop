import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Box,
  Card,
  Flex,
  Group,
  Stack,
  Text,
  Title,
  Button,
} from '@mantine/core'
import { ArrowRight, EyeOff, Search, Shield, Sparkles } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import Badge from '@/components/Badge'
import { useRulepackStore } from './store'
import FeatureFlagGate from './FeatureFlagGate'

const SEARCH_DEBOUNCE_MS = 300

function FeatureBadge({ icon, label }) {
  const Icon = icon
  return (
    <Badge variant="light" color="gray" leftSection={<Icon size={12} />}>
      {label}
    </Badge>
  )
}

function RulepackRow({ rulepack, isLast, onConfigure }) {
  const hasDataMasking = (rulepack.data_masking_rules ?? []).length > 0
  const hasGuardrails = (rulepack.guardrail_rules ?? []).length > 0
  const hasAiAnalyzer = (rulepack.ai_session_analyzer_rules ?? []).length > 0
  const anyBadge = hasDataMasking || hasGuardrails || hasAiAnalyzer

  return (
    <Box
      p="lg"
      style={
        isLast
          ? undefined
          : { borderBottom: '1px solid var(--mantine-color-default-border)' }
      }
    >
      <Flex align="center" gap="md">
        <Stack gap={4} style={{ flex: 1, minWidth: 0 }}>
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
                <FeatureBadge icon={Sparkles} label="AI Session Analyzer" />
              )}
            </Group>
          )}
        </Stack>
        <Group gap="xs" wrap="nowrap">
          <Button onClick={() => onConfigure(rulepack.id)}>
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
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const searching = useMemo(
    () => input.trim().length > 0 || listSearch.length > 0,
    [input, listSearch],
  )

  return (
    <Stack gap="xl">
      <Stack gap="sm">
        <Title order={1}>Rulepacks</Title>
        <Text size="md" c="dimmed">
          Bundle configurations and apply them to many connections at once.
        </Text>
      </Stack>

      <TextInput
        placeholder="Search rulepacks by name"
        value={input}
        onChange={handleSearchChange}
        leftSection={<Search size={16} />}
      />

      {showLoader ? (
        <PageLoader h={300} />
      ) : list.length === 0 ? (
        <Card padding="xl" withBorder>
          <Stack gap={4} align="center" ta="center">
            <Text fw={600}>
              {searching ? 'No rulepacks match your search' : 'No rulepacks yet'}
            </Text>
            <Text size="sm" c="dimmed">
              {searching
                ? 'Try a different search term.'
                : 'Rulepacks bundle related rules and let you apply them to many connections at once.'}
            </Text>
          </Stack>
        </Card>
      ) : (
        <Card padding={0} withBorder>
          {list.map((rulepack, idx) => (
            <RulepackRow
              key={rulepack.id}
              rulepack={rulepack}
              isLast={idx === list.length - 1}
              onConfigure={(id) => navigate(`/rulepacks/${id}`)}
            />
          ))}
        </Card>
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

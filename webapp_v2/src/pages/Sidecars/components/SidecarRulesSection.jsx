import { useEffect, useState } from 'react'
import { Divider, Group, Paper, Stack, Table, Title, Text, Select, Button } from '@mantine/core'
import { Shield, Eye, Brain, Trash2, Plus } from 'lucide-react'
import { sidecarsService } from '@/services/sidecars'
import { guardrailsService } from '@/services/guardrails'
import { dataMaskingService } from '@/services/dataMasking'
import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { showSnackbar } from '@/utils/snackbar'

export default function SidecarRulesSection({ sidecar }) {
  const [mappings, setMappings] = useState({ guardrail: [], datamasking: [], ai_analyzer: [] })
  const [guardrails, setGuardrails] = useState([])
  const [dataMaskingRules, setDataMaskingRules] = useState([])
  const [aiRules, setAiRules] = useState([])
  const [loading, setLoading] = useState(true)
  const [submitting, setSaving] = useState(false)

  // Form State
  const [ruleType, setRuleType] = useState('guardrail')
  const [listenerName, setListenerName] = useState('')
  const [selectedRuleId, setSelectedRuleId] = useState('')

  useEffect(() => {
    const listeners = sidecar.configuration?.listeners ?? []
    if (listeners.length > 0 && !listenerName) {
      setListenerName(listeners[0].name)
    }
  }, [sidecar])

  const fetchMappingsAndRules = async () => {
    try {
      setLoading(true)
      const [mapsRes, grRes, dmRes, aiRes] = await Promise.all([
        sidecarsService.getRules(sidecar.id),
        guardrailsService.list(),
        dataMaskingService.list(),
        aiSessionAnalyzerService.listRules(),
      ])

      setMappings(mapsRes.data || { guardrail: [], datamasking: [], ai_analyzer: [] })
      setGuardrails(grRes.data || [])
      setDataMaskingRules(dmRes.data || [])
      setAiRules(aiRes.data?.data || aiRes.data || [])
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to load fleet configurations.',
        description: err.response?.data?.message ?? err.message,
      })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (sidecar?.id) {
      fetchMappingsAndRules()
    }
  }, [sidecar?.id])

  const handleCreateMapping = async () => {
    if (!selectedRuleId) {
      showSnackbar({ level: 'error', text: 'Please select a rule.' })
      return
    }
    setSaving(true)
    try {
      await sidecarsService.createRuleMapping(sidecar.id, {
        rule_type: ruleType,
        rule_id: selectedRuleId,
        listener_name: listenerName,
      })
      showSnackbar({ level: 'success', text: 'Rule mapped successfully!' })
      setSelectedRuleId('')
      fetchMappingsAndRules()
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to create rule mapping.',
        description: err.response?.data?.message ?? err.message,
      })
    } finally {
      setSaving(false)
    }
  }

  const handleDeleteMapping = async (type, ruleId, name) => {
    try {
      await sidecarsService.deleteRuleMapping(sidecar.id, {
        rule_type: type,
        rule_id: ruleId,
        listener_name: name,
      })
      showSnackbar({ level: 'success', text: 'Rule mapping removed.' })
      fetchMappingsAndRules()
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to remove rule mapping.',
        description: err.response?.data?.message ?? err.message,
      })
    }
  }

  const getRuleOptions = () => {
    if (ruleType === 'guardrail') {
      return guardrails.map((r) => ({ value: r.id, label: r.name }))
    }
    if (ruleType === 'datamasking') {
      return dataMaskingRules.map((r) => ({ value: r.id, label: r.name }))
    }
    if (ruleType === 'ai_analyzer') {
      return aiRules.map((r) => ({ value: r.id || r.name, label: r.name }))
    }
    return []
  }

  const getListenerOptions = () => {
    const list = []
    const listeners = sidecar.configuration?.listeners ?? []
    listeners.forEach((l) => {
      list.push({ value: l.name, label: l.name })
    })
    return list
  }

  if (loading) {
    return <Text size="sm">Loading rule mappings...</Text>
  }

  const hasMappings =
    mappings.guardrail.length > 0 ||
    mappings.data_masking?.length > 0 ||
    mappings.datamasking?.length > 0 ||
    mappings.ai_analyzer.length > 0

  const dmList = mappings.datamasking || mappings.data_masking || []

  return (
    <Paper withBorder radius="md" p="lg">
      <Stack gap="lg">
        <Title order={3}>Fleet Feature Configuration</Title>
        <Text size="sm" c="dimmed">
          Configure security features (Guardrails, Data Masking, AI Session Analyzer) once and distribute them directly
          to this sidecar's listeners dynamically.
        </Text>

        <Divider />

        <Stack gap="md">
          <Text fw={600} size="sm">Add Rule Mapping</Text>
          <Group align="flex-end" gap="md" wrap="wrap">
            <Select
              label="Feature Type"
              w={200}
              data={[
                { value: 'guardrail', label: 'Guardrail' },
                { value: 'datamasking', label: 'Data Masking' },
                { value: 'ai_analyzer', label: 'AI Session Analyzer' },
              ]}
              value={ruleType}
              onChange={(val) => {
                setRuleType(val)
                setSelectedRuleId('')
              }}
            />
            <Select
              label="Target Listener"
              w={200}
              data={getListenerOptions()}
              value={listenerName}
              onChange={setListenerName}
            />
            <Select
              label="Select Rule"
              placeholder="Pick a rule to apply"
              w={300}
              data={getRuleOptions()}
              value={selectedRuleId}
              onChange={setSelectedRuleId}
              searchable
            />
            <Button
              onClick={handleCreateMapping}
              loading={submitting}
              leftSection={<Plus size={16} />}
            >
              Apply Rule
            </Button>
          </Group>
        </Stack>

        <Divider />

        <Stack gap="sm">
          <Text fw={600} size="sm">Active Security Rules</Text>
          {!hasMappings ? (
            <Text size="sm" c="dimmed">No centralized security rules are active on this sidecar.</Text>
          ) : (
            <Table highlightOnHover withTableBorder>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Feature Type</Table.Th>
                  <Table.Th>Rule Name</Table.Th>
                  <Table.Th>Target Listener</Table.Th>
                  <Table.Th w={80}></Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {mappings.guardrail.map((m) => (
                  <Table.Tr key={`gr-${m.rule_id}-${m.listener_name}`}>
                    <Table.Td>
                      <Group gap="xs">
                        <Shield size={16} color="var(--mantine-color-red-6)" />
                        <Text size="sm">Guardrail</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td fw={600}>{m.rule_name || m.rule_id}</Table.Td>
                    <Table.Td>
                      <Text size="sm">{m.listener_name === '*' ? '* (All)' : m.listener_name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Button
                        variant="subtle"
                        color="red"
                        onClick={() => handleDeleteMapping('guardrail', m.rule_id, m.listener_name)}
                        px={4}
                      >
                        <Trash2 size={16} />
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}

                {dmList.map((m) => (
                  <Table.Tr key={`dm-${m.rule_id}-${m.listener_name}`}>
                    <Table.Td>
                      <Group gap="xs">
                        <Eye size={16} color="var(--mantine-color-blue-6)" />
                        <Text size="sm">Data Masking</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td fw={600}>{m.rule_name || m.rule_id}</Table.Td>
                    <Table.Td>
                      <Text size="sm">{m.listener_name === '*' ? '* (All)' : m.listener_name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Button
                        variant="subtle"
                        color="red"
                        onClick={() => handleDeleteMapping('datamasking', m.rule_id, m.listener_name)}
                        px={4}
                      >
                        <Trash2 size={16} />
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}

                {mappings.ai_analyzer.map((m) => (
                  <Table.Tr key={`ai-${m.rule_id}-${m.listener_name}`}>
                    <Table.Td>
                      <Group gap="xs">
                        <Brain size={16} color="var(--mantine-color-teal-6)" />
                        <Text size="sm">AI Session Analyzer</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td fw={600}>{m.rule_name || m.rule_id}</Table.Td>
                    <Table.Td>
                      <Text size="sm">{m.listener_name === '*' ? '* (All)' : m.listener_name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Button
                        variant="subtle"
                        color="red"
                        onClick={() => handleDeleteMapping('ai_analyzer', m.rule_id, m.listener_name)}
                        px={4}
                      >
                        <Trash2 size={16} />
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
        </Stack>
      </Stack>
    </Paper>
  )
}

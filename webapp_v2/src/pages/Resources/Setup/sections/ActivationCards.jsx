import { useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Image, Paper, SimpleGrid, Stack, Text, Title, Group } from '@mantine/core'
import { Check, ShieldCheck, VenetianMask } from 'lucide-react'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { EVENTS } from '@/services/analytics'
import { useUserStore } from '@/stores/useUserStore'
import { buildCards, ctaPath } from '../activation'
import { useResourceSetupStore } from '../store'

const SALES_URL = 'https://hoop.dev/meet'
const INTERCOM_MESSAGE = 'I want to upgrade my current plan'

function FeatureIcon({ card }) {
  if (card.enabled) {
    return (
      <Avatar size="lg" radius="md" color="green" variant="light">
        <Check size={20} aria-hidden="true" />
      </Avatar>
    )
  }
  if (card.feature === 'ai-analyzer') {
    return (
      <Avatar size="lg" radius="md" color="gray" variant="light">
        <Image src="/icons/ai-analyser-icon.svg" alt="" w={15} h={15} />
      </Avatar>
    )
  }
  const Icon = card.feature === 'guardrails' ? ShieldCheck : VenetianMask
  const color = card.locked ? 'gray' : card.feature === 'guardrails' ? 'indigo' : 'violet'
  return (
    <Avatar size="lg" radius="md" color={color} variant={card.locked ? 'light' : 'filled'}>
      <Icon size={20} aria-hidden="true" />
    </Avatar>
  )
}

// The three activation cards of the success step, admins only: they link to
// admin pages. Nothing renders until every request has settled.
export default function ActivationCards() {
  const navigate = useNavigate()
  const setup = useResourceSetupStore((s) => s.setup)
  const activation = useResourceSetupStore((s) => s.activation)
  const loadActivation = useResourceSetupStore((s) => s.loadActivation)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const free = useUserStore((s) => s.isFreeLicense)
  const hasRedactCredentials = useUserStore((s) => s.hasRedactCredentials)
  const redactProvider = useUserStore((s) => s.redactProvider)
  const trackEvent = useUserStore((s) => s.trackEvent)
  const showIntercomMessage = useUserStore((s) => s.showIntercomMessage)

  useEffect(() => {
    if (isAdmin) loadActivation()
  }, [isAdmin, loadActivation])

  const roles = useMemo(() => activation?.roles ?? [], [activation])
  const cards = useMemo(() => {
    if (!activation) return []
    return buildCards({
      subtype: setup.subtype,
      free,
      gates: {
        guardrails: hasRedactCredentials,
        masking: redactProvider === 'mspresidio',
        'ai-analyzer': activation.providerOk,
      },
      lists: activation.lists,
      connectionIds: roles.map((r) => r.id),
      connectionNames: roles.map((r) => r.name),
    })
  }, [activation, roles, setup.subtype, free, hasRedactCredentials, redactProvider])

  if (!isAdmin || !activation) return null

  const onCta = (card) => {
    trackEvent(EVENTS.ACTIVATION_FEATURE_CTA, {
      feature: card.feature,
      surface: 'resource-success',
      kind: card.cta.kind,
    })
    // The admin already saw the pitch: skip the analyzer's first-visit promotion.
    if (card.feature === 'ai-analyzer' && card.cta.kind !== 'talk-to-sales') {
      try {
        localStorage.setItem('ai-session-analyzer-promotion-seen', 'true')
      } catch {
        // Blocked storage only means the promotion shows once.
      }
    }
    if (card.cta.kind === 'talk-to-sales') {
      if (!showIntercomMessage(INTERCOM_MESSAGE)) {
        window.open(SALES_URL, '_blank', 'noopener,noreferrer')
      }
      return
    }
    navigate(
      ctaPath(card, {
        connectionIds: roles.map((r) => r.id),
        connectionNames: roles.map((r) => r.name),
      }),
    )
  }

  return (
    <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="lg">
      {cards.map((card) => (
        <Paper key={card.feature} withBorder radius="md" p="lg">
          <Stack gap="md" h="100%">
            <Group justify="space-between" align="flex-start">
              <FeatureIcon card={card} />
              {card.badge && (
                <Badge variant="light" color={card.badge.color} tag chip>
                  {card.badge.label}
                </Badge>
              )}
            </Group>
            <Stack gap={4} flex={1}>
              <Title order={4} c={card.locked ? 'dimmed' : undefined}>
                {card.title}
              </Title>
              <Text size="sm" c="dimmed">
                {card.description}
              </Text>
            </Stack>
            <Button variant="light" fullWidth onClick={() => onCta(card)}>
              {card.cta.label}
            </Button>
          </Stack>
        </Paper>
      ))}
    </SimpleGrid>
  )
}

import { Box, Flex, Image, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'
import Button from '@/components/Button'

/**
 * Generic feature promotion panel — a React port of the CLJS
 * `webapp.features.promotion/feature-promotion`. Fills a feature page when the
 * feature is available but empty (`mode="empty-state"`) or gated behind a plan
 * upgrade (`mode="upgrade-plan"`). Split layout: marketing copy + feature
 * highlights on the left, an illustration on the right.
 *
 * Props:
 * - featureName       Feature label, e.g. "Live Data Masking".
 * - mode              'empty-state' (default) | 'upgrade-plan'. Only changes the
 *                     default primary-button text when `primaryText` is omitted.
 * - image             Filename under /images/illustrations/; omitted → placeholder.
 * - description       Short paragraph under the heading.
 * - featureItems      [{ icon: ReactNode, title, description }] highlight cards.
 * - onPrimaryClick    Primary button handler (button hidden when absent).
 * - primaryText       Primary button label (defaults from `mode`/`featureName`).
 * - extraInformation  Secondary note (e.g. deprecated-provider warning).
 * - docsHref          External docs URL — renders a docs link button when set.
 * - docsText          Docs link button label.
 */
export default function FeaturePromotion({
  featureName,
  mode = 'empty-state',
  image,
  description,
  featureItems = [],
  onPrimaryClick,
  primaryText,
  extraInformation,
  docsHref,
  docsText,
}) {
  const isEmptyState = mode === 'empty-state'
  const buttonText =
    primaryText || (isEmptyState ? `Create new ${featureName}` : 'Request demo')

  return (
    <Flex h="100%" mih="28rem">
      <Stack w="50%" miw="50%" p="xxlAlt" gap="xxlAlt" justify="center">
        <Stack gap="sm">
          <Title order={1} fw={700}>
            {`Get more with ${featureName}`}
          </Title>
          {description && (
            <Text size="xl" c="dimmed">
              {description}
            </Text>
          )}
        </Stack>

        {featureItems.length > 0 && (
          <Stack gap="xlAlt">
            {featureItems.map((item) => (
              <Flex key={item.title} align="flex-start" gap="lgAlt">
                <ThemeIcon size={52} radius="md" variant="light" color="indigo">
                  {item.icon}
                </ThemeIcon>
                <Stack gap={2}>
                  <Text size="lg" fw={700}>
                    {item.title}
                  </Text>
                  <Text size="md">{item.description}</Text>
                </Stack>
              </Flex>
            ))}
          </Stack>
        )}

        {extraInformation && (
          <Text size="sm" c="dimmed">
            {extraInformation}
          </Text>
        )}

        {docsHref && docsText && (
          <Button
            component="a"
            href={docsHref}
            target="_blank"
            rel="noopener noreferrer"
            variant="default"
            size="md"
            w="fit-content"
            rightSection={<ArrowUpRight size={16} />}
          >
            {docsText}
          </Button>
        )}

        {onPrimaryClick && buttonText && (
          <Button onClick={onPrimaryClick} size="md" w="fit-content">
            {buttonText}
          </Button>
        )}
      </Stack>

      <Box w="50%" miw="50%" bg="blue.0">
        {image && (
          <Image
            src={`/images/illustrations/${image}`}
            alt={`${featureName} illustration`}
            h="100%"
            w="100%"
            fit="cover"
          />
        )}
      </Box>
    </Flex>
  )
}

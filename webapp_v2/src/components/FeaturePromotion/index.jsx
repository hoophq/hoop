import { Box, Flex, Image, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'
import Button from '@/components/Button'

/**
 * Split promotion panel (copy + highlights left, illustration right) shown when a
 * feature is empty (`mode="empty-state"`) or gated (`mode="upgrade-plan"`).
 * `image` is a filename under /images/illustrations/.
 *
 * Usage:
 *   <FeaturePromotion
 *     featureName="Live Data Masking"
 *     image="data-masking-promotion.png"
 *     description="..."
 *     featureItems={[{ icon: <FolderLock />, title: '...', description: '...' }]}
 *     onPrimaryClick={goCreate}
 *     primaryText="Configure"
 *   />
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

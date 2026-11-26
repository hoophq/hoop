import { useEffect, useMemo, useState } from 'react'
import { Flex, Group, Image, Input, Paper, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import Checkbox from '@/components/Checkbox'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import EmptyState from '@/layout/EmptyState'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useUserStore } from '@/stores/useUserStore'
import DetailModal from './DetailModal'
import {
  catalogEntries,
  categoryLabel,
  entryBadges,
  entryIconUrl,
  filterEntries,
  filterOptions,
  groupByCategory,
  popularEntries,
  sectionTitle,
} from './entries'
import classes from './Catalog.module.css'

function EntryIcon({ entry }) {
  const [failed, setFailed] = useState(false)
  if (failed) {
    return (
      <Avatar size={24} variant="filled">
        {entry.id.charAt(0)}
      </Avatar>
    )
  }
  return <Image src={entryIconUrl(entry)} alt="" w={24} h={24} onError={() => setFailed(true)} />
}

function EntryCard({ entry, onOpen }) {
  return (
    <UnstyledButton className={classes.card} onClick={() => onOpen(entry)}>
      <Paper withBorder p="md" w={176} h={110} className={classes.surface}>
        <Stack justify="space-between" gap="xs" h="100%">
          <Group justify="space-between" gap="xs" wrap="nowrap">
            <EntryIcon entry={entry} />
            {entryBadges(entry).map((badge) => (
              <Badge key={badge.text} color={badge.color} variant="filled">
                {badge.text}
              </Badge>
            ))}
          </Group>
          <Text size="sm" fw={500} lineClamp={2}>
            {entry.name}
          </Text>
        </Stack>
      </Paper>
    </UnstyledButton>
  )
}

function Section({ title, entries, onOpen }) {
  return (
    <Stack gap="md">
      <Title order={2}>{title}</Title>
      <Group gap="md">
        {entries.map((entry) => (
          <EntryCard key={entry.id} entry={entry} onOpen={onOpen} />
        ))}
      </Group>
    </Stack>
  )
}

function Filters({ search, onSearch, categories, onCategories, tags, onToggleTag, options }) {
  return (
    <Stack
      component="aside"
      aria-label="Filters"
      gap="xlAlt"
      w={{ base: '100%', md: 280 }}
      pos={{ base: 'static', md: 'sticky' }}
      top={0}
    >
      <TextInput
        label="Search"
        placeholder="Resources or keywords"
        value={search}
        onChange={(event) => onSearch(event.currentTarget.value)}
      />
      <Checkbox.Group label="Categories" value={categories} onChange={onCategories}>
        <Stack gap="sm" mt="sm">
          {options.categories.map((category) => (
            <Checkbox key={category} value={category} label={categoryLabel(category)} />
          ))}
        </Stack>
      </Checkbox.Group>
      <Input.Wrapper label="Tags" labelElement="div">
        <Group gap="xs" mt="sm">
          {options.tags.slice(0, 15).map((tag) => {
            const on = tags.has(tag)
            return (
              <Badge
                key={tag}
                component="button"
                type="button"
                size="chip"
                tag
                variant={on ? 'filled' : 'inactive'}
                aria-pressed={on}
                className={classes.toggle}
                onClick={() => onToggleTag(tag)}
              >
                {tag}
              </Badge>
            )
          })}
        </Group>
      </Input.Wrapper>
    </Stack>
  )
}

// /resource-catalog outside onboarding: every type the wizard creates.
export default function ResourceCatalog() {
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const error = useConnectionsMetadataStore((s) => s.error)
  const load = useConnectionsMetadataStore((s) => s.load)
  const featureFlags = useUserStore((s) => s.featureFlags)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const [search, setSearch] = useState('')
  const [categories, setCategories] = useState([])
  const [tags, setTags] = useState(() => new Set())
  const [selected, setSelected] = useState(null)
  const [opened, setOpened] = useState(false)

  useEffect(() => {
    load()
  }, [load])

  const entries = useMemo(
    () => catalogEntries(metadata?.connections, featureFlags),
    [metadata, featureFlags],
  )
  const options = useMemo(() => filterOptions(entries), [entries])
  const filters = { search, categories: new Set(categories), tags }
  const popular = popularEntries(entries, filters)
  const groups = groupByCategory(filterEntries(entries, filters))

  if (!metadata) {
    if (!error) return <PageLoader h={400} />
    return (
      <EmptyState
        title="Could not load the resource catalog"
        description={error}
        action={{ label: 'Try again', onClick: load }}
      />
    )
  }

  const toggleTag = (tag) =>
    setTags((current) => {
      const next = new Set(current)
      if (next.has(tag)) next.delete(tag)
      else next.add(tag)
      return next
    })
  const open = (entry) => {
    setSelected(entry)
    setOpened(true)
  }

  return (
    <Flex direction={{ base: 'column', md: 'row' }} gap="xxlAlt" align="flex-start">
      <Filters
        search={search}
        onSearch={setSearch}
        categories={categories}
        onCategories={setCategories}
        tags={tags}
        onToggleTag={toggleTag}
        options={options}
      />
      <Stack gap="xxlAlt" flex={1} miw={0}>
        <Stack gap="sm">
          <Title order={1}>Getting Started</Title>
          <Text size="md" c="dimmed">
            Set up your environment by selecting your Resource type.
          </Text>
        </Stack>
        {popular.length > 0 && <Section title="Popular" entries={popular} onOpen={open} />}
        {groups.map(([category, list]) => (
          <Section key={category} title={sectionTitle(category)} entries={list} onOpen={open} />
        ))}
        {groups.length === 0 && (
          <EmptyState
            compact
            title="No Resource types match your filters"
            description="Try clearing the filters on the left."
          />
        )}
      </Stack>
      <DetailModal entry={selected} opened={opened} onClose={() => setOpened(false)} isAdmin={isAdmin} />
    </Flex>
  )
}

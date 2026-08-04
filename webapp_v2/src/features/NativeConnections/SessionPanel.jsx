import { useState } from 'react'
import { Divider, Group, ScrollArea, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Tabs from '@/components/Tabs'
import { pickCredentialRenderer } from './credentials'
import { SessionTimer } from './components/SessionTimer'
import { normalizeSubtype, openRdpWebClient } from './helpers'
import classes from './NativeConnections.module.css'

/**
 * The established-connection view: credentials for the connection's subtype,
 * a live countdown when the credential is bounded, and the teardown actions.
 */
export function SessionPanel({ credentials, onDisconnect }) {
  const rule = pickCredentialRenderer(credentials.connection_subtype)
  const [tab, setTab] = useState(rule.tabs?.[0]?.value ?? 'credentials')

  // Capitalised aliases so JSX treats them as components rather than DOM tags.
  const SoleRenderer = rule.tabs?.length === 1 ? rule.tabs[0].render : null
  const BareRenderer = rule.tabs ? null : rule.render

  const subtype = normalizeSubtype(credentials.connection_subtype)
  const rendererProps = {
    credentials: credentials.connection_credentials ?? {},
    connectionName: credentials.connection_name,
  }

  return (
    <Stack gap="md">
      {credentials.expire_at ? (
        <Group gap="xs">
          <Text fz="sm" c="dimmed">
            Connection established, time left:
          </Text>
          <SessionTimer expireAt={credentials.expire_at} />
        </Group>
      ) : (
        <Text fz="sm" c="dimmed">
          Connection established
        </Text>
      )}

      {/* Bounded and scrollable: credential blocks (claude-code especially) are
          long enough to push every other role out of the drawer otherwise. The
          footer sits outside, so Disconnect is always reachable. */}
      <ScrollArea.Autosize className={classes.sessionScroll} type="auto" offsetScrollbars>
        {/* Renderers are mounted as elements, not invoked as functions, so a
            renderer that later needs a hook does not break the rules of hooks. */}
        {rule.tabs ? (
          rule.tabs.length === 1 ? (
            // A single tab is just a label — render the body directly.
            <SoleRenderer {...rendererProps} />
          ) : (
            <Tabs value={tab} onChange={setTab}>
              <Tabs.List aria-label="Connection methods">
                {rule.tabs.map((t) => (
                  <Tabs.Tab key={t.value} value={t.value}>
                    {t.label}
                  </Tabs.Tab>
                ))}
              </Tabs.List>
              {rule.tabs.map((t) => {
                const TabRenderer = t.render
                return (
                  <Tabs.Panel key={t.value} value={t.value} pt="md">
                    <TabRenderer {...rendererProps} />
                  </Tabs.Panel>
                )
              })}
            </Tabs>
          )
        ) : (
          <BareRenderer {...rendererProps} />
        )}
      </ScrollArea.Autosize>

      <Divider />

      {/* Revoke (invalidate the token outright) exists on the store but is
          deliberately not surfaced here — it was never rendered in the CLJS UI
          either, and adding a new destructive action alongside the redesign is
          a separate product decision. */}
      <Group justify="flex-end" gap="xs">
        {subtype === 'rdp' && (
          <Button
            size="xs"
            onClick={() => openRdpWebClient(credentials.connection_credentials?.username)}
          >
            Open web client
          </Button>
        )}
        <Button color="red" size="xs" onClick={onDisconnect}>
          Disconnect
        </Button>
      </Group>
    </Stack>
  )
}

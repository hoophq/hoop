import { useState } from 'react'
import { Divider, Group, ScrollArea, Stack } from '@mantine/core'
import Button from '@/components/Button'
import Tabs from '@/components/Tabs'
import { pickCredentialRenderer } from './credentials'
import { normalizeSubtype, openRdpWebClient } from './helpers'
import classes from './NativeConnections.module.css'

/**
 * Bounded, scrollable region for the credential body.
 *
 * Credential blocks (claude-code is five instruction blocks) would otherwise
 * push every other role out of the drawer and take the disconnect footer with
 * them.
 */
function ScrollableBody({ children }) {
  return (
    <ScrollArea.Autosize className={classes.sessionScroll} type="auto" offsetScrollbars>
      {children}
    </ScrollArea.Autosize>
  )
}

/**
 * The established-connection view: credentials for the connection's subtype and
 * the teardown actions. The row itself carries the session status, so there is
 * no "connection established" line here.
 */
export function SessionPanel({ credentials, onDisconnect }) {
  const rule = pickCredentialRenderer(credentials.connection_subtype)
  const [tab, setTab] = useState(rule.tabs?.[0]?.value ?? 'credentials')

  // Capitalised aliases so JSX treats them as components rather than DOM tags.
  // Renderers are mounted as elements, not invoked as functions, so a renderer
  // that later needs a hook does not break the rules of hooks.
  const SoleRenderer = rule.tabs?.length === 1 ? rule.tabs[0].render : null
  const BareRenderer = rule.tabs ? null : rule.render

  const subtype = normalizeSubtype(credentials.connection_subtype)
  const rendererProps = {
    credentials: credentials.connection_credentials ?? {},
    connectionName: credentials.connection_name,
  }

  return (
    <Stack gap="lg">
      {rule.tabs && rule.tabs.length > 1 ? (
        // The tab list stays outside the scroll area so it stays pinned while
        // the panel body scrolls underneath it.
        <Tabs value={tab} onChange={setTab}>
          <Tabs.List aria-label="Connection methods">
            {rule.tabs.map((t) => (
              <Tabs.Tab key={t.value} value={t.value}>
                {t.label}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          <ScrollableBody>
            {rule.tabs.map((t) => {
              const TabRenderer = t.render
              return (
                <Tabs.Panel key={t.value} value={t.value} pt="md">
                  <TabRenderer {...rendererProps} />
                </Tabs.Panel>
              )
            })}
          </ScrollableBody>
        </Tabs>
      ) : (
        <ScrollableBody>
          {SoleRenderer ? <SoleRenderer {...rendererProps} /> : <BareRenderer {...rendererProps} />}
        </ScrollableBody>
      )}

      <Divider />

      {/* Revoke (invalidate the token outright) exists on the store but is
          deliberately not surfaced here — it was never rendered in the CLJS UI
          either, and adding a new destructive action alongside the redesign is
          a separate product decision. */}
      <Group justify="flex-end" gap="sm">
        {subtype === 'rdp' && (
          <Button
            size="sm"
            variant="default"
            onClick={() => openRdpWebClient(credentials.connection_credentials?.username)}
          >
            Open web client
          </Button>
        )}
        <Button color="red" size="sm" onClick={onDisconnect}>
          Disconnect
        </Button>
      </Group>
    </Stack>
  )
}

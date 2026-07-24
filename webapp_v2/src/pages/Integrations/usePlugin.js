import { useCallback, useEffect, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { pluginsService } from '@/services/plugins'

function errorMessage(error, fallback) {
  return error?.response?.data?.message || error?.message || fallback
}

/**
 * State and mutations for a single plugin (slack, webhooks).
 *
 * A 404 on GET means the plugin was never installed for this org: the page
 * still renders (empty connections) and the first mutation creates the
 * plugin via POST /plugins instead of PUT.
 */
export function usePlugin(pluginName) {
  const [plugin, setPlugin] = useState(null)
  const [installed, setInstalled] = useState(false)
  const [status, setStatus] = useState('loading')
  const [mutating, setMutating] = useState(false)

  const fetchPlugin = useCallback(async () => {
    try {
      const res = await pluginsService.get(pluginName)
      setPlugin(res.data)
      setInstalled(true)
      setStatus('ready')
    } catch (error) {
      if (error?.response?.status === 404) {
        setPlugin({ name: pluginName, connections: [] })
        setInstalled(false)
        setStatus('ready')
      } else {
        setStatus('error')
        notifications.show({
          message: errorMessage(error, 'Failed to load plugin.'),
          color: 'red',
        })
      }
    }
  }, [pluginName])

  useEffect(() => {
    fetchPlugin()
  }, [fetchPlugin])

  const saveConnections = useCallback(
    async (nextConnections, successMessage) => {
      setMutating(true)
      try {
        const payload = { name: pluginName, connections: nextConnections }
        if (installed) {
          await pluginsService.update(pluginName, payload)
        } else {
          await pluginsService.create(payload)
          setInstalled(true)
        }
        notifications.show({ message: successMessage, color: 'green' })
        await fetchPlugin()
        return true
      } catch (error) {
        notifications.show({
          message: errorMessage(error, 'Failed to update plugin.'),
          color: 'red',
        })
        return false
      } finally {
        setMutating(false)
      }
    },
    [pluginName, installed, fetchPlugin]
  )

  // PUT replaces the whole connections array, so every entry must be resent
  // as {id, config} — dropping config here would wipe it on the server.
  const currentConnections = useCallback(
    () => (plugin?.connections ?? []).map((c) => ({ id: c.id, config: c.config })),
    [plugin]
  )

  const toggleConnection = useCallback(
    (connection, enabled) => {
      const rest = currentConnections().filter((c) => c.id !== connection.id)
      const next = enabled ? [...rest, { id: connection.id }] : rest
      return saveConnections(next, `Connection ${enabled ? 'enabled' : 'disabled'}.`)
    },
    [currentConnections, saveConnections]
  )

  const updateConnectionConfig = useCallback(
    (connectionId, config) => {
      const next = currentConnections().map((c) => (c.id === connectionId ? { id: c.id, config } : c))
      return saveConnections(next, 'Configuration saved.')
    },
    [currentConnections, saveConnections]
  )

  const saveEnvvars = useCallback(
    async (envvars, successMessage = 'Configuration saved.') => {
      setMutating(true)
      try {
        if (!installed) {
          await pluginsService.create({ name: pluginName, connections: [] })
          setInstalled(true)
        }
        await pluginsService.updateConfig(pluginName, envvars)
        notifications.show({ message: successMessage, color: 'green' })
        await fetchPlugin()
        return true
      } catch (error) {
        notifications.show({
          message: errorMessage(error, 'Failed to save configuration.'),
          color: 'red',
        })
        return false
      } finally {
        setMutating(false)
      }
    },
    [pluginName, installed, fetchPlugin]
  )

  return {
    plugin,
    installed,
    status,
    mutating,
    toggleConnection,
    updateConnectionConfig,
    saveEnvvars,
  }
}

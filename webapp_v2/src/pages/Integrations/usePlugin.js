import { useCallback, useEffect, useState } from 'react'
import { showSnackbar } from '@/utils/snackbar'
import { pluginsService } from '@/services/plugins'
import { connectionsService } from '@/services/connections'

function errorDescription(error) {
  return error?.response?.data?.message || error?.message
}

/**
 * State and mutations for a single plugin page (slack, webhooks). Loads the
 * plugin and the workspace connections together so pages render one loader.
 *
 * A 404 on GET means the plugin was never installed for this org: the page
 * still renders (empty connections) and the first mutation creates the
 * plugin via POST /plugins instead of PUT.
 */
export function usePlugin(pluginName) {
  const [plugin, setPlugin] = useState(null)
  const [connections, setConnections] = useState([])
  const [installed, setInstalled] = useState(false)
  const [status, setStatus] = useState('loading')
  const [mutating, setMutating] = useState(false)

  const fetchPlugin = useCallback(async () => {
    try {
      const res = await pluginsService.get(pluginName)
      setPlugin(res.data)
      setInstalled(true)
    } catch (error) {
      if (error?.response?.status === 404) {
        setPlugin({ name: pluginName, connections: [] })
        setInstalled(false)
      } else {
        throw error
      }
    }
  }, [pluginName])

  useEffect(() => {
    async function loadPage() {
      try {
        const [, connectionsData] = await Promise.all([
          fetchPlugin(),
          connectionsService.getConnections(),
        ])
        setConnections(
          Array.isArray(connectionsData) ? connectionsData : (connectionsData?.items ?? [])
        )
        setStatus('ready')
      } catch (error) {
        setStatus('error')
        showSnackbar({
          level: 'error',
          text: 'Failed to load plugin.',
          description: errorDescription(error),
        })
      }
    }
    loadPage()
  }, [fetchPlugin])

  const refreshPlugin = useCallback(async () => {
    try {
      await fetchPlugin()
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to refresh plugin.',
        description: errorDescription(error),
      })
    }
  }, [fetchPlugin])

  const saveConnections = useCallback(
    async (nextConnections, successText) => {
      setMutating(true)
      try {
        const payload = { name: pluginName, connections: nextConnections }
        if (installed) {
          await pluginsService.update(pluginName, payload)
        } else {
          await pluginsService.create(payload)
          setInstalled(true)
        }
        showSnackbar({ level: 'success', text: successText })
        await refreshPlugin()
        return true
      } catch (error) {
        showSnackbar({
          level: 'error',
          text: 'Failed to update plugin.',
          description: errorDescription(error),
        })
        return false
      } finally {
        setMutating(false)
      }
    },
    [pluginName, installed, refreshPlugin]
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
    async (envvars, successText = 'Configuration saved.') => {
      setMutating(true)
      try {
        if (!installed) {
          await pluginsService.create({ name: pluginName, connections: [] })
          setInstalled(true)
        }
        await pluginsService.updateConfig(pluginName, envvars)
        showSnackbar({ level: 'success', text: successText })
        await refreshPlugin()
        return true
      } catch (error) {
        showSnackbar({
          level: 'error',
          text: 'Failed to save configuration.',
          description: errorDescription(error),
        })
        return false
      } finally {
        setMutating(false)
      }
    },
    [pluginName, installed, refreshPlugin]
  )

  return {
    plugin,
    connections,
    installed,
    status,
    mutating,
    toggleConnection,
    updateConnectionConfig,
    saveEnvvars,
  }
}

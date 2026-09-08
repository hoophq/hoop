import { useEffect, useState } from 'react'
import { connectionsService } from '@/services/connections'

// A sidecar lists the names of the connections it fronts (SidecarResponse
// .connections); the type behind each name is on the connection itself. One
// non-paginated GET /connections (the full array, RBAC join not applied) is
// enough to join by name for the table and the details card. A failed lookup
// leaves the map empty and the names still render, without an icon.
export function useConnectionsByName() {
  const [byName, setByName] = useState(() => new Map())

  useEffect(() => {
    let cancelled = false
    connectionsService
      .getConnections()
      .then((connections) => {
        if (cancelled) return
        setByName(new Map((connections ?? []).map((c) => [c.name, c])))
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  return byName
}

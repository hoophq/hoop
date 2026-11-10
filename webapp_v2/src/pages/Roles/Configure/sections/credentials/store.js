import { createContext, useContext, useState } from 'react'
import { useStore } from 'zustand'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

// Configure Role mounts no provider, so the renderers read its store.
// The resource setup wizard provides one store per role.
const CredentialsStoreContext = createContext(useConfigureRoleStore)

export const CredentialsStoreProvider = CredentialsStoreContext.Provider

export function useCredentialsStore(selector) {
  return useStore(useContext(CredentialsStoreContext), selector)
}

// Form state that is not a secret (a picked provider or server). A wizard role
// store keeps it, so it survives the roles step unmounting on Back.
export function useFormState(key, initial) {
  const [local, setLocal] = useState(initial)
  const stored = useCredentialsStore((s) => s.formState?.[key])
  const setFormState = useCredentialsStore((s) => s.setFormState)
  if (!setFormState) return [local, setLocal]
  return [stored === undefined ? initial : stored, (value) => setFormState(key, value)]
}

import { createContext, useContext } from 'react'
import { useStore } from 'zustand'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

// Configure Role mounts no provider, so the renderers read its store.
// The resource setup wizard provides one store per role.
const CredentialsStoreContext = createContext(useConfigureRoleStore)

export const CredentialsStoreProvider = CredentialsStoreContext.Provider

export function useCredentialsStore(selector) {
  return useStore(useContext(CredentialsStoreContext), selector)
}

import { create } from 'zustand'

export const useUserStore = create((set) => ({
  user: null,
  isAdmin: false,
  isFreeLicense: true,
  gatewayVersion: null,
  loading: false,

  setUser: (user) => set({ user, isAdmin: !!user?.is_admin }),
  setServerInfo: (serverInfo) => {
    const license = serverInfo?.license_info
    const isFreeLicense = !(license?.is_valid && license?.type === 'enterprise')
    set({ isFreeLicense, gatewayVersion: serverInfo?.version || null })
  },
  setLoading: (loading) => set({ loading }),
  clear: () => set({ user: null, isAdmin: false, isFreeLicense: true, gatewayVersion: null }),
}))

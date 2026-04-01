import { create } from 'zustand'

export const useUserStore = create((set) => ({
  user: null,
  isAdmin: false,
  isFreeLicense: true,
  loading: false,

  setUser: (user) => set({ user, isAdmin: !!user?.is_admin }),
  setServerInfo: (serverInfo) => {
    const license = serverInfo?.license_info
    const isFreeLicense = !(license?.is_valid && license?.type === 'enterprise')
    set({ isFreeLicense })
  },
  setLoading: (loading) => set({ loading }),
  clear: () => set({ user: null, isAdmin: false, isFreeLicense: true }),
}))

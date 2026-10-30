import { authService } from '@/services/auth'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'

// Spread, like the live events stream, so tabs do not retry at once.
const retryDelay = () => 3000 + Math.random() * 5000
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

const storedToken = () => localStorage.getItem('jwt-token')

// The account a JWT names (`sub`), or null for an opaque token: some OIDC
// providers hand the browser one.
function tokenSubject(token) {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    return JSON.parse(atob(payload)).sub || null
  } catch {
    return null
  }
}

// The id of the user `token` belongs to, by the gateway. A failure that is not
// a refusal (the network) is tried again while `token` is still the stored
// one: until it answers, this tab cannot tell whose requests it sends.
async function ownerOf(token) {
  for (;;) {
    try {
      const user = await authService.getUserForToken(token)
      return user?.id ?? null
    } catch (error) {
      // A refused token: the next request of this tab signs it out.
      if (error.status === 401 || error.status === 403) return null
    }
    await sleep(retryDelay())
    if (storedToken() !== token) return null
  }
}

async function onStorage(event) {
  const token = event.newValue
  if (event.key !== 'jwt-token' || !token || !useUserStore.getState().user) return
  // A newer token replaced this one before or while it is checked: its own
  // event decides.
  if (storedToken() !== token) return

  // Two JWTs: their `sub` tells the account, with no request.
  const signedIn = tokenSubject(useAuthStore.getState().token)
  const stored = tokenSubject(token)
  if (signedIn && stored) {
    if (stored !== signedIn) window.location.reload()
    return
  }

  // An opaque token: ask the gateway whose it is.
  const owner = await ownerOf(token)
  if (storedToken() !== token) return
  const shown = useUserStore.getState().user?.id
  if (owner && shown && owner !== shown) window.location.reload()
}

// Another tab stored a token. Every request reads the token from localStorage,
// so this tab now sends that one. Another account (a sign-in in that tab):
// reload, so every store and the CLJS app start over as that account. The
// same account (a renewal in that tab): nothing changes. The check never
// writes a token, or two tabs would answer each other's writes without end.
// Its own module, which imports both stores, so neither store imports the other.
export function watchAccountSwitch() {
  window.addEventListener('storage', onStorage)
}

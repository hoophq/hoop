import { createContext, useContext } from 'react'

// TEMPORARY: A/B switch for the SourcedInput redesign. Two variants are
// in flight — `single-outline` (picker inside the input's outline,
// CLJS-like) and `glued-siblings` (picker + input as two touching
// components). The CredentialsTab mounts a Switch above the credentials
// body that flips this Provider's value so every SourcedInput on the
// page renders the chosen variant. Once the user picks a winner we
// delete this file, drop the Provider, and inline the winning render
// into SourcedInput/index.jsx.
//
// Default is `single-outline` — it matches the CLJS reference the user
// asked us to mirror.
export const VARIANT_SINGLE_OUTLINE = 'single-outline'
export const VARIANT_GLUED_SIBLINGS = 'glued-siblings'

const SourcedInputVariantContext = createContext(VARIANT_SINGLE_OUTLINE)

export const SourcedInputVariantProvider = SourcedInputVariantContext.Provider

export function useSourcedInputVariant() {
  return useContext(SourcedInputVariantContext)
}

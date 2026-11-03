(ns webapp.shell)

(defn react-shell?
  "True when the React shell hosts this window. Set per window by
   webapp_v2/src/main.jsx before the CLJS bundle loads. Never read the shared
   localStorage key: another tab can remove it while this one boots."
  []
  (boolean (.-__hoopReactShellPresent js/window)))

(defn cljs-visible?
  "True when the shell hosts this window AND the CLJS view is mounted, not
   parked. Use it in late async callbacks: after the user moves to a React
   page they must not redirect it."
  []
  (boolean (and (.-__hoopReactShellPresent js/window)
                (.-__hoopReactShellCljsVisible js/window))))

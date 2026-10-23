(ns webapp.shell)

(defn react-shell?
  "True when the React shell hosts this window. Set per window by
   webapp_v2/src/main.jsx before the CLJS bundle loads. Never read the shared
   localStorage key: another tab can remove it while this one boots."
  []
  (boolean (.-__hoopReactShellPresent js/window)))

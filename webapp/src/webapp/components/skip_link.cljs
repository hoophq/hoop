(ns webapp.components.skip-link
  "Skip link component for keyboard accessibility.
   Provides quick navigation shortcuts for keyboard and screen reader users.")

(defn main
  "Convenience component for skip links (hidden until focused).

   Props:
   - :target-selector - CSS selector of the target element (required)
   - :text - Link text to display (required)
   - :on-click - Optional custom click handler (receives event)

   Examples:
   ```clojure
   ;; Basic offscreen skip link (appears on focus)
   [skip-link/main {:target-selector \"#main-content\"
                    :text \"Skip to main content\"}]
   ```"
  [{:keys [target-selector text on-click]}]
  (let [default-click-handler (fn [e]
                                (.preventDefault e)
                                (when-let [target-elem (.querySelector js/document target-selector)]
                                  (.focus target-elem)))
        click-handler (or on-click default-click-handler)

        base-classes "bg-primary-9 text-white px-3 py-1 rounded focus:outline-none focus:ring-2 focus:ring-primary-11 hover:bg-primary-11"]

    [:a {:href target-selector
         :tabIndex "0"
         :class (str base-classes " fixed left-1/2 -top-96 focus:top-1/2 z-50")
         :on-click click-handler}
     text]))

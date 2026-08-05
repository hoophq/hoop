(ns webapp.webclient.components.side-panel
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]
   ["allotment" :refer [Allotment]]))

(defn side-panel [{:keys [title content aria-label]}]
  [:> Box {:class "h-full w-full bg-gray-1 border-l border-gray-3 overflow-y-auto"
           :role "complementary"
           :aria-label (or aria-label title "Side panel")}
   (when title
     [:> Flex {:justify "between"
               :align "center"
               :class "px-4 py-3 border-b border-gray-3"}
      [:> Text {:size "3" :weight "bold" :class "text-gray-12"} title]])

   [:> Box
    content]])

(defn with-panel [show-panel? content panel]
  ;; Subtracts the terminal toolbar from the space this splitter gets. The
  ;; literal used to be 4rem — the toolbar's old height — which left the editor
  ;; pane 22px taller than its container once the toolbar shrank, pushing the
  ;; log-area footer off screen. Token lives in tailwind.config.js.
  [:> Flex {:class "h-[calc(100%-theme(spacing.terminal-header))]"}
   [:> Allotment {:key (str "allotment-" show-panel?)
                  :defaultSizes [750 250]
                  :horizontal true}
    [:> Box {:class "flex-grow transition-all duration-300"}
     content]
    (when show-panel?
      [side-panel panel])]])

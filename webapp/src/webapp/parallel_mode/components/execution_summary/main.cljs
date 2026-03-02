(ns webapp.parallel-mode.components.execution-summary.main
  (:require
   ["@radix-ui/themes" :refer [Flex Dialog ScrollArea]]
   [re-frame.core :as rf]
   [webapp.parallel-mode.components.execution-summary.header :as header]
   [webapp.parallel-mode.components.execution-summary.progress-bar :as progress-bar]
   [webapp.parallel-mode.components.execution-summary.running-list :as running-list]
   [webapp.parallel-mode.components.execution-summary.tabs :as tabs]))

(defn execution-summary-modal []
  (let [execution-state (rf/subscribe [:parallel-mode/execution-state])]
    (fn []
      (let [has-data? (seq (:data @execution-state))]
        [:> Dialog.Root
         {:open has-data?}
         [:> Dialog.Content
          {:maxWidth "90vw"
           :minHeight "90vh"
           :maxHeight "90vh"
           :class "p-0"
           :aria-label "Parallel execution summary"
           :aria-describedby "execution-summary-description"
           :onEscapeKeyDown (fn [e] (.preventDefault e))
           :onPointerDownOutside (fn [e] (.preventDefault e))}
          [:span {:id "execution-summary-description" :class "sr-only"}
           "Execution results for parallel mode. Press Escape is disabled while execution is running."]
          [:> ScrollArea
           {:type "auto"
            :scrollbars "vertical"
            :class "h-[90vh]"}
           [:> Flex {:direction "column" :class "min-h-full"}
            ;; Header - sticky at top
            [header/main]

            ;; Progress bar - sticky below header, collapses on fade
            [progress-bar/main]

            ;; Running list - scrolls normally, collapses on fade
            [running-list/main]

            ;; Success/Error tabs - becomes sticky after fade out
            [tabs/main]]]]]))))

(ns webapp.parallel-mode.components.execution-summary.main
  (:require
   ["@radix-ui/themes" :refer [Box Dialog]]
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
           :maxHeight "90vh"
           :class "p-0"
           :onEscapeKeyDown (fn [e] (.preventDefault e))
           :onPointerDownOutside (fn [e] (.preventDefault e))}
          [:> Box {:class "h-[85vh] flex flex-col"}
           ;; Header
           [header/main]
           
           ;; Progress bar (fades out when complete)
           [progress-bar/main]
           
           ;; Running list (fades out when complete)
           [running-list/main]
           
           ;; Success/Error tabs
           [tabs/main]]]]))))


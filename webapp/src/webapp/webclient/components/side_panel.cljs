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
  ;; Fills whatever the terminal toolbar leaves. This used to subtract a
  ;; hardcoded 4rem — the toolbar's old height — which left the editor pane
  ;; taller than its container once the toolbar shrank and pushed the log-area
  ;; footer off screen. flex-1 removes the constant entirely.
  [:> Flex {:class "flex-1 min-h-0"}
   [:> Allotment {:key (str "allotment-" show-panel?)
                  :defaultSizes [750 250]
                  :horizontal true}
     ;; h-full so the content inside can size against the Allotment pane,
     ;; which is the nearest ancestor with a definite height.
     [:> Box {:class "flex-grow h-full transition-all duration-300"}
     content]
    (when show-panel?
      [side-panel panel])]])

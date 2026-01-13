(ns webapp.parallel-mode.components.execution-summary.tabs
  (:require
   ["@radix-ui/themes" :refer [Box Flex Tabs Text]]
   [re-frame.core :as rf]
   [webapp.parallel-mode.components.execution-summary.success-list :as success-list]
   [webapp.parallel-mode.components.execution-summary.error-list :as error-list]))

(defn main []
  (let [success-count (rf/subscribe [:parallel-mode/success-count])
        error-count (rf/subscribe [:parallel-mode/error-count])
        active-tab (rf/subscribe [:parallel-mode/active-tab])]
    (fn []
      [:> Box {:class "flex-1 overflow-hidden"}
       [:> Tabs.Root
        {:value (or @active-tab "success")
         :onValueChange #(rf/dispatch [:parallel-mode/set-active-tab %])
         :class "h-full flex flex-col"}

        ;; Tab headers
        [:> Tabs.List
         {:class "px-6 border-b border-gray-6"}
         [:> Tabs.Trigger
          {:value "success"
           :class "px-4 py-3"}
          [:> Flex {:align "center" :gap "2"}
           [:> Text {:size "2" :weight "medium"}
            (str "Success (" @success-count ")")]]]

         [:> Tabs.Trigger
          {:value "error"
           :class "px-4 py-3"}
          [:> Flex {:align "center" :gap "2"}
           [:> Text {:size "2" :weight "medium"}
            (str "Error (" @error-count ")")]]]]

        ;; Tab content
        [:> Box {:class "flex-1 overflow-y-auto"}
         [:> Tabs.Content
          {:value "success"
           :class "h-full"}
          [success-list/main]]

         [:> Tabs.Content
          {:value "error"
           :class "h-full"}
          [error-list/main]]]]])))


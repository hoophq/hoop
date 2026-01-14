(ns webapp.parallel-mode.components.execution-summary.tabs
  (:require
   ["@radix-ui/themes" :refer [Box Flex Tabs Text]]
   [re-frame.core :as rf]
   [webapp.parallel-mode.components.execution-summary.success-list :as success-list]
   [webapp.parallel-mode.components.execution-summary.error-list :as error-list]))

(defn main []
  (let [success-count (rf/subscribe [:parallel-mode/success-count])
        error-count (rf/subscribe [:parallel-mode/error-count])
        active-tab (rf/subscribe [:parallel-mode/active-tab])
        fade-out? (rf/subscribe [:parallel-mode/should-fade-out?])
        is-running? (rf/subscribe [:parallel-mode/is-executing?])]
    (fn []
      [:> Box {:class (str "flex-1 transition-opacity duration-500 "
                           (when @is-running? "opacity-0 h-0 overflow-hidden")
                           (when (and @fade-out? (not @is-running?)) "opacity-100"))}
       [:> Tabs.Root
        {:value (or @active-tab "success")
         :onValueChange #(rf/dispatch [:parallel-mode/set-active-tab %])
         :class "flex flex-col"}

        ;; Tab headers - sticky after fade out
        [:> Tabs.List
         {:class (str "transition-all duration-300 bg-white "
                      (when @fade-out? "sticky top-20 z-30"))}
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

        ;; Tab content - scrolls normally
        [:> Box
         [:> Tabs.Content
          {:value "success"
           :class "rounded-lg border border-gray-3 bg-white"}
          [success-list/main]]

         [:> Tabs.Content
          {:value "error"
           :class "rounded-lg border border-gray-3 bg-white"}
          [error-list/main]]]]])))

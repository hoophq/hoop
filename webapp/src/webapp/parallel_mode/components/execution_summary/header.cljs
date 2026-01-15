(ns webapp.parallel-mode.components.execution-summary.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton]]
   ["lucide-react" :refer [Search Link X]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(defn main []
  (let [search-term (rf/subscribe [:parallel-mode/execution-search-term])
        is-running? (rf/subscribe [:parallel-mode/is-executing?])
        batch-id (rf/subscribe [:parallel-mode/batch-id])]
    (fn []
      [:> Box {:class "sticky top-0 z-50 bg-white p-6"}
       [:> Flex {:justify "between" :align "center" :gap "4"}
        [:> Flex {:align "center" :gap "5"}
         [:> Heading {:size "5" :weight "bold" :class "text-gray-12"}
          "Execution Summary"]

         [:> Button
          {:size "2"
           :variant "soft"
           :color "gray"
           :highContrast true
           :disabled (nil? @batch-id)
           :onClick (when @batch-id
                      #(let [url (str (.. js/window -location -origin) "/sessions/filtered?batch_id=" @batch-id)]
                         (-> js/navigator
                             .-clipboard
                             (.writeText url)
                             (.then (fn []
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text "Link copied to clipboard!"}])))
                             (.catch (fn [err]
                                       (js/console.error "Failed to copy:" err)
                                       (rf/dispatch [:show-snackbar {:level :error
                                                                     :text "Failed to copy link"}]))))))}
          [:> Link {:size 16}]
          "Share"]]

        [:> Flex {:align "center" :gap "3"}
         ;; Search
         [:> Box {:class "w-64"}
          [forms/input
           {:size "2"
            :not-margin-bottom? true
            :placeholder "Search by resource role or type"
            :value @search-term
            :on-change #(rf/dispatch [:parallel-mode/set-execution-search (.. % -target -value)])
            :start-adornment [:> Search {:size 16}]}]]

         [:> IconButton
          {:size "2"
           :variant "soft"
           :color "gray"
           :highContrast true
           :onClick #(if @is-running?
                       (rf/dispatch [:parallel-mode/request-cancel])
                       (rf/dispatch [:parallel-mode/clear-execution]))}
          [:> X {:size 18}]]]]])))

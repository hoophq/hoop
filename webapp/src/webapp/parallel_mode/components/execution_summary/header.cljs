(ns webapp.parallel-mode.components.execution-summary.header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton TextField]]
   ["lucide-react" :refer [Search Share2 X]]
   [re-frame.core :as rf]))

(defn main []
  (let [search-term (rf/subscribe [:parallel-mode/execution-search-term])
        is-running? (rf/subscribe [:parallel-mode/is-executing?])]
    (fn []
      [:> Box {:class "border-b border-gray-6 px-6 py-4 bg-gray-1"}
       [:> Flex {:justify "between" :align "center" :gap "4"}
        ;; Left side - Title
        [:> Heading {:size "5" :weight "bold" :class "text-gray-12"}
         "Execution Summary"]
        
        ;; Right side - Search + Share + Close
        [:> Flex {:align "center" :gap "3"}
         ;; Search
         [:> TextField.Root
          {:size "2"
           :placeholder "Search..."
           :value @search-term
           :onChange #(rf/dispatch [:parallel-mode/set-execution-search (.. % -target -value)])
           :class "w-64"}
          [:> TextField.Slot
           [:> Search {:size 16}]]]
         
         ;; Share button (disabled for now)
         [:> Button
          {:size "2"
           :variant "soft"
           :color "gray"
           :disabled true}
          [:> Share2 {:size 16}]
          "Share"]
         
         ;; Close button
         [:> IconButton
          {:size "2"
           :variant "ghost"
           :color "gray"
           :onClick #(if @is-running?
                       (rf/dispatch [:parallel-mode/request-cancel])
                       (rf/dispatch [:parallel-mode/clear-execution]))}
          [:> X {:size 18}]]]]])))


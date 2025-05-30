(ns webapp.components.paginated-dropdown
  (:require
   ["@radix-ui/themes" :refer [Box IconButton DropdownMenu Text]]
   ["lucide-react" :refer [ArrowRight ArrowLeft]]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn paginated-dropdown
  "A paginated dropdown with search functionality

   Parameters:
   - options: list of options [{:value \"id\" :label \"text\" :description \"description\"}]
   - loading?: boolean indicating if it's loading data
   - on-search: function called when user types in the search box
   - on-page-change: function called when user changes page
   - on-select: function called when user selects an option
   - selected-value: currently selected value
   - placeholder: text to show when nothing is selected
   - total-items: total number of items
   - current-page: current page
   - items-per-page: items per page"
  []
  (let [open? (r/atom false)
        search-term (r/atom "")
        debounce-timer (atom nil)]
    (fn [{:keys [options loading? on-search on-page-change
                 on-select selected-value placeholder
                 total-items current-page items-per-page]}]
      [:> Box {:class "relative"}

       [:> DropdownMenu.Root {:dir "ltr"}
        [:> DropdownMenu.Trigger
         [:> Box {:class "rt-reset rt-SelectTrigger rt-r-size-3 rt-variant-surface w-full "}
          [:> Box
           (if selected-value
             (if-let [selected-option (first (filter #(= (:value %) selected-value) options))]
               [:span {:class "rt-SelectTriggerInner"}
                (:label selected-option)]

               [:span {:class "rt-SelectTriggerInner opacity-50"}
                (or placeholder "Select an option")])

             [:span {:class "rt-SelectTriggerInner opacity-50"}
              (or placeholder "Select an option")])]
          [:> DropdownMenu.TriggerIcon]]]
        [:> DropdownMenu.Content {:size "2" :class "w-[820px]"}
         [:> Box {:class "mt-1 rounded bg-white"}
          ;; Search field
          [:> Box {:class ""}
           [:> Box {:class "relative"}

            [forms/input
             {:type "text"
              :placeholder "Search options..."
              :value @search-term
              :on-change (fn [e]
                           (let [value (.. e -target -value)]
                             (reset! search-term value)
                             (when @debounce-timer
                               (js/clearTimeout @debounce-timer))
                             (reset! debounce-timer
                                     (js/setTimeout #(on-search value) 300))))}]]]

          ;; Options list
          (cond
            loading?
            [:> Box {:class "flex items-center justify-center h-full"}
             [:> Text {:size "2" :class "text-gray-12"}
              "Loading..."]]

            (empty? options)
            [:> Box {:class "flex items-center justify-center h-full"}
             [:> Text {:size "2" :class "text-gray-12"}
              "No options found"]]

            :else
            [:> Box {:class "max-h-60 overflow-y-auto"}
             (for [option options]
               ^{:key (:value option)}
               [:> DropdownMenu.Item
                {:class "px-4 py-2 hover:bg-gray-100 cursor-pointer"
                 :on-click #(do (on-select (:value option))
                                (reset! open? false))}
                [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                 (:label option)]
                (when (:description option)
                  [:> Text {:size "2" :class "text-gray-12"}
                   (:description option)])])])

          ;; Pagination
          [:> Box {:class "flex items-center justify-between px-4 py-2 border-t text-sm"}
           [:> Text {:size "1" :class "text-gray-11"}
            (str "Showing "
                 (inc (* (dec current-page) items-per-page)) "-"
                 (min (* current-page items-per-page) total-items)
                 " of " total-items)]
           [:> Box {:class "flex items-center space-x-2"}
            [:> IconButton
             {:variant "outline"
              :disabled (= current-page 1)
              :class (when (= current-page 1) "cursor-not-allowed")
              :on-click #(when (> current-page 1)
                           (on-page-change (dec current-page)))}
             [:> ArrowLeft {:size 16}]]
            [:> Text {:size "2" :class "text-gray-12"}
             (str current-page "/" (js/Math.ceil (/ total-items items-per-page)))]
            [:> IconButton
             {:variant "outline"
              :disabled (>= (* current-page items-per-page) total-items)
              :class (when (>= (* current-page items-per-page) total-items) "cursor-not-allowed")
              :on-click #(when (< (* current-page items-per-page) total-items)
                           (on-page-change (inc current-page)))}
             [:> ArrowRight {:size 16}]]]]]]]])))

(ns webapp.components.paginated-searchbox
  (:require
   ["@radix-ui/themes" :refer [IconButton Text]]
   ["lucide-react" :refer [ArrowRight ArrowLeft ChevronDown]]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main
  "A paginated dropdown that works inside modals using fixed positioning

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
   - items-per-page: items per page
   - required?: boolean indicating if selection is required
   - name: form field name for validation"
  []
  (let [open? (r/atom false)
        search-term (r/atom "")
        debounce-timer (atom nil)
        dropdown-ref (atom nil)
        trigger-ref (atom nil)
        dropdown-position (r/atom {:top 0 :left 0 :width 0})
        selected-label (r/atom nil)]
    (r/create-class
     {:component-did-mount
      (fn []
        ;; Add click outside listener
        (.addEventListener js/document "click"
                           (fn [e]
                             (when (and @open? @dropdown-ref @trigger-ref)
                               (let [target (.-target e)
                                     inside-dropdown? (.contains @dropdown-ref target)
                                     inside-trigger? (.contains @trigger-ref target)]
                                 (when (not (or inside-dropdown? inside-trigger?))
                                   (reset! open? false)))))))

      :component-will-unmount
      (fn []
        ;; Clean up timer
        (when @debounce-timer
          (js/clearTimeout @debounce-timer)))

      :reagent-render
      (fn [{:keys [options loading? on-search on-page-change
                   on-select selected-value placeholder
                   total-items current-page items-per-page
                   required? name]}]

        ;; Update selected label when we have the option in current options
        (when selected-value
          (when-let [selected-option (first (filter #(= (:value %) selected-value) options))]
            (when (not= @selected-label (:label selected-option))
              (reset! selected-label (:label selected-option)))))

        ;; Reset selected label if no selected value
        (when (and (not selected-value) @selected-label)
          (reset! selected-label nil))

        ;; Calculate position when opening
        (when (and @open? @trigger-ref)
          (let [rect (.getBoundingClientRect @trigger-ref)]
            (reset! dropdown-position
                    {:top (+ (.-bottom rect) 8 (.-scrollY js/window))
                     :left (+ (.-left rect) (.-scrollX js/window))
                     :width (.-width rect)})))

        [:div {:class "relative w-full"}

         ;; Invisible input for form validation (allows validation popup to show)
         (when (and required? name)
           [:input {:type "text"
                    :style {:position "absolute"
                            :opacity 0
                            :pointer-events "none"
                            :width "1px"
                            :height "1px"
                            :z-index -1
                            :top "40px"
                            :left "50%"}
                    :name name
                    :value (or selected-value "")
                    :required true
                    :tab-index -1}])

         ;; Trigger button
         [:button {:type "button"
                   :ref #(reset! trigger-ref %)
                   :class "rt-reset rt-SelectTrigger rt-r-size-3 rt-variant-surface w-full flex items-center justify-between px-3 py-2 border border-gray-6 rounded-md bg-white hover:bg-gray-2 focus:outline-none focus:ring-2 focus:ring-blue-5"
                   :on-click #(do (.preventDefault %)
                                  (swap! open? not))}
          [:span {:class (if selected-value
                           "rt-SelectTriggerInner"
                           "rt-SelectTriggerInner opacity-50")}
           (if selected-value
             (or @selected-label placeholder "Select an option")
             (or placeholder "Select an option"))]
          [:> ChevronDown {:size 16 :class (str "transition-transform " (when @open? "rotate-180"))}]]

         ;; Fixed position dropdown content
         (when @open?
           [:div {:class "dropdown-overlay"
                  :style {:position "fixed"
                          :top 0
                          :left 0
                          :right 0
                          :bottom 0
                          :z-index 9999
                          :pointer-events "none"}}
            [:div {:class "bg-white shadow-lg border border-gray-6 rounded-md"
                   :ref #(reset! dropdown-ref %)
                   :style {:position "absolute"
                           :top (str (:top @dropdown-position) "px")
                           :left (str (:left @dropdown-position) "px")
                           :width (str (:width @dropdown-position) "px")
                           :pointer-events "auto"}}

             ;; Search field
             [:div {:class "p-3 border-b border-gray-4"}
              [forms/input
               {:type "text"
                :id "cmdb-search-options"
                :placeholder "Search options..."
                :value @search-term
                :not-margin-bottom? true
                :on-change (fn [e]
                             (let [value (.. e -target -value)]
                               (reset! search-term value)
                               (when @debounce-timer
                                 (js/clearTimeout @debounce-timer))
                               (reset! debounce-timer
                                       (js/setTimeout #(on-search value) 300))))}]]

             ;; Options list
             (cond
               loading?
               [:div {:class "flex items-center justify-center h-32"}
                [:> Text {:size "2" :class "text-gray-12"}
                 "Loading..."]]

               (empty? options)
               [:div {:class "flex items-center justify-center h-32"}
                [:> Text {:size "2" :class "text-gray-12"}
                 "No options found"]]

               :else
               [:div {:class "max-h-60 overflow-y-auto"}
                (for [option options]
                  ^{:key (:value option)}
                  [:div {:class "px-4 py-2 hover:bg-gray-2 cursor-pointer border-b border-gray-3 last:border-b-0"
                         :on-click #(do (reset! selected-label (:label option))
                                        (on-select (:value option))
                                        (reset! open? false))}
                   [:div {:class "flex flex-col"}
                    [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                     (:label option)]
                    (when (:description option)
                      [:> Text {:size "1" :class "text-gray-11 mt-1"}
                       (:description option)])]])])

             ;; Pagination
             [:div {:class "flex items-center justify-between px-4 py-3 border-t border-gray-4 bg-gray-1"}
              [:> Text {:size "1" :class "text-gray-11"}
               (str "Showing "
                    (inc (* (dec current-page) items-per-page)) "-"
                    (min (* current-page items-per-page) total-items)
                    " of " total-items)]
              [:div {:class "flex items-center space-x-2"}
               [:> IconButton
                {:variant "outline"
                 :size "1"
                 :disabled (= current-page 1)
                 :class (when (= current-page 1) "cursor-not-allowed")
                 :on-click (fn [e]
                             (.preventDefault e)
                             (when (> current-page 1)
                               (on-page-change (dec current-page))))}
                [:> ArrowLeft {:size 14}]]
               [:> Text {:size "1" :class "text-gray-12"}
                (str current-page "/" (js/Math.ceil (/ total-items items-per-page)))]
               [:> IconButton
                {:variant "outline"
                 :size "1"
                 :disabled (>= (* current-page items-per-page) total-items)
                 :class (when (>= (* current-page items-per-page) total-items) "cursor-not-allowed")
                 :on-click (fn [e]
                             (.preventDefault e)
                             (when (< (* current-page items-per-page) total-items)
                               (on-page-change (inc current-page))))}
                [:> ArrowRight {:size 14}]]]]]])])})))

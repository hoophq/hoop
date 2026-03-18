(ns webapp.components.connection-filter
  (:require
   ["@radix-ui/themes" :refer [Popover Button TextField Text Flex Box]]
   ["lucide-react" :refer [Check Rotate3d Search X]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [clojure.string :as cs]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

(defn connection-filter
  "Reusable connection filter component with Radix UI and infinite scroll.

   Props:
   - :selected (string) - Currently selected connection name
   - :on-select (fn [conn-name]) - Callback when a connection is selected
   - :on-clear (fn []) - Callback when filter is cleared
   - :label (string, optional) - Label for the button (default: 'Resource Role')
   - :placeholder (string, optional) - Placeholder for search input (default: 'Search resource roles')"
  [_]
  (let [open? (r/atom false)
        search-term (r/atom "")
        debounce-timer (r/atom nil)
        connections (rf/subscribe [:connections->pagination])]

    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])

    (fn [{:keys [selected on-select on-clear label placeholder]
          :or {label "Resource Role"
               placeholder "Search resource roles"}}]
      (let [connections-data (or (:data @connections) [])
            connections-loading? (or (:loading @connections) false)
            has-more? (or (:has-more? @connections) false)
            current-page (or (:current-page @connections) 1)
            active-search (or (:active-search @connections) "")
            close! #(reset! open? false)]
        [:> Popover.Root {:open @open?
                          :on-open-change #(reset! open? %)}
         [:> Popover.Trigger {:asChild true}
          [:> Button {:size "2"
                      :variant (if selected "soft" "surface")
                      :color "gray"
                      :class "gap-2"}
           [:> Rotate3d {:size 14}]
           [:> Text {:size "2" :weight "medium"}
            (if selected selected label)]
           (when selected
             [:<>
              [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-[--accent-9]"}
               [:span {:class "text-white text-xs font-bold"} "1"]]
              [:> X {:size 14
                     :on-click (fn [e]
                                 (.stopPropagation e)
                                 (on-clear)
                                 (close!))}]])]]
         [:> Popover.Content {:size "2" :style {:width "360px" :max-height "400px"}}
          [:> Box
           (when selected
             [:> Box {:mb "2" :pb "2" :class "border-b border-[--gray-a6]"}
              [:> Flex {:align "center"
                        :gap "2"
                        :class "cursor-pointer text-[--gray-11] hover:bg-[--gray-a3] rounded px-3 py-2"
                        :on-click (fn []
                                    (on-clear)
                                    (close!))}
               [:> Text {:size "2"} "Clear filter"]]])
           [:> Box {:mb "2"}
            [:> TextField.Root {:placeholder placeholder
                                :value @search-term
                                :onChange (fn [e]
                                            (let [value (-> e .-target .-value)
                                                  trimmed (cs/trim value)]
                                              (reset! search-term value)
                                              (when @debounce-timer
                                                (js/clearTimeout @debounce-timer))
                                              (reset! debounce-timer
                                                      (js/setTimeout
                                                       (fn []
                                                         (rf/dispatch
                                                          [:connections/get-connections-paginated
                                                           (cond-> {:page 1 :force-refresh? true}
                                                             (seq trimmed) (assoc :search trimmed))]))
                                                       300))))}
             [:> TextField.Slot
              [:> Search {:size 14}]]]]
           (if (seq connections-data)
             [:> Box {:class "max-h-72 overflow-y-auto"}
              [infinite-scroll
               {:on-load-more (fn []
                                (when (and (not connections-loading?) has-more?)
                                  (let [next-page (inc current-page)
                                        next-request (cond-> {:page next-page
                                                              :force-refresh? false}
                                                       (not (cs/blank? active-search))
                                                       (assoc :search active-search))]
                                    (rf/dispatch [:connections/get-connections-paginated next-request]))))
                :has-more? has-more?
                :loading? connections-loading?}
               (doall
                (for [conn connections-data]
                  ^{:key (:name conn)}
                  [:> Flex {:align "center"
                            :justify "between"
                            :gap "2"
                            :class "cursor-pointer hover:bg-[--gray-a3] rounded px-3 py-2"
                            :on-click (fn []
                                        (on-select (:name conn))
                                        (close!))}
                   [:> Flex {:align "center" :gap "2"}
                    [:figure {:class "w-4 h-4 flex-shrink-0"}
                     [:img {:src (connection-constants/get-connection-icon conn)
                            :class "w-full"}]]
                    [:> Text {:size "2" :class "truncate"} (:name conn)]]
                   (when (= (:name conn) selected)
                     [:> Check {:size 14}])]))
               (when connections-loading?
                 [:> Box {:py "2"}
                  [:> Text {:size "1" :class "text-[--gray-11] text-center"}
                   "Loading..."]])]]
             [:> Box {:px "3" :py "4"}
              [:> Text {:size "1" :class "text-[--gray-11] italic"}
               (if (seq @search-term)
                 "No resource roles found"
                 "No resource roles available")]])]]]))))

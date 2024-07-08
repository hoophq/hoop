(ns webapp.webclient.exec-multiples-connections.select-list
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.searchbox :as searchbox]))

(def atom-run-popover-open? (r/atom false))

(defn main [{:keys [connection-name
                    run-connections-list-selected
                    run-connections-list-suggested
                    run-connections-list-rest
                    atom-run-connections-list
                    atom-filtered-run-connections-list]}]
  [:div {:class "absolute bottom-full w-full z-40"}
   [:div {:class "fixed inset-0 w-full h-full"
          :on-click #(reset! atom-run-popover-open? (not @atom-run-popover-open?))}]
   [:div {:class "absolute z-50 max-w-80 bg-white h-connection-selector border shadow top-10 -right-2 mt-1"}
    [:div {:id "popover-containe"
           :class "w-max h-full overflow-y-auto"}
     [:ul {:class "max-h-96 overflow-y-auto mb-4 border-b border-gray-200 p-regular"}
      [:span {:class "text-xs text-gray-400 font-normal"}
       "Running in"]
      [:li {:class "font-bold text-sm text-gray-900 truncate py-small"}
       connection-name]
      (for [connection run-connections-list-selected]
        ^{:key (:name connection)}
        [:li {:class "font-bold text-sm text-gray-900 truncate cursor-pointer hover:bg-gray-50 py-small"
              :on-click #(rf/dispatch [:editor-plugin->toggle-select-run-connection (:name connection)])}
         (:name connection)])]

     [:div {:class "overflow-y-auto text-gray-900 w-80 "}
      [:div {:class "px-regular"}
       [searchbox/main
        {:options (:data @atom-run-connections-list)
         :on-change-results-cb #(rf/dispatch [:editor-plugin->set-filtered-run-connection-list %])
         :searchable-keys [:name]
         :hide-results-list true
         :placeholder "Search"
         :loading? (= (:status @atom-run-connections-list) :loading)
         :name "connections-run-editor-search"
         :clear? true
         :selected (-> @atom-run-connections-list :name)}]]

      (when (empty? (filterv #(not (:selected %)) @atom-filtered-run-connections-list))
        [:div {:class "p-regular"}
         [:span {:class "text-xs text-gray-500 font-normal"}
          "There's no connection matching your search."]])

      (when-not (empty? run-connections-list-suggested)
        [:ul {:class "p-regular border-b border-gray-200"}
         [:span {:class "text-xs text-gray-400 font-normal"}
          "Suggested"]
         (for [connection run-connections-list-suggested]
           ^{:key (:name connection)}
           [:li {:on-click (fn []
                             (when (= "online" (:status connection))
                               (rf/dispatch [:editor-plugin->toggle-select-run-connection (:name connection)])))
                 :class (str "py-small cursor-pointer text-sm flex items-center gap-small "
                             "font-normal "
                             (if (= "offline" (:status connection))
                               "text-gray-500"
                               "text-gray-900 hover:bg-gray-50"))}
            [:span {:class "block truncate"} (:name connection)]
            (when (:selected connection)
              [:div
               [:> hero-micro-icon/CheckIcon {:class "h-4 w-4 shrink-0 text-gray-900"
                                              :aria-hidden "true"}]])
            (when (= "offline" (:status connection))
              [:span
               "(Offline)"])])])

      (when-not (empty? run-connections-list-rest)
        [:ul {:class "p-regular"}
         [:span {:class "text-xs text-gray-400 font-normal"}
          "Connections"]
         (for [connection run-connections-list-rest]
           ^{:key (:name connection)}
           [:li {:on-click (fn []
                             (when (= "online" (:status connection))
                               (rf/dispatch [:editor-plugin->toggle-select-run-connection (:name connection)])))
                 :class (str "py-small cursor-pointer text-sm flex items-center gap-small "
                             "font-normal "
                             (if (= "offline" (:status connection))
                               "text-gray-500"
                               "text-gray-900 hover:bg-gray-50"))}
            [:span {:class "block truncate"} (:name connection)]
            (when (:selected connection)
              [:div
               [:> hero-micro-icon/CheckIcon {:class "h-4 w-4 shrink-0 text-gray-900"
                                              :aria-hidden "true"}]])
            (when (= "offline" (:status connection))
              [:span
               "(Offline)"])])])]]]])

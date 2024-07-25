(ns webapp.webclient.aside.main
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.searchbox :as searchbox]
            [webapp.webclient.aside.connections-list :as connections-list]
            [webapp.webclient.aside.connections-running-list :as connections-running-list]
            [webapp.webclient.aside.metadata :as metadata]
            [webapp.webclient.runbooks.list :as runbooks-list]))

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])]
    (rf/dispatch [:runbooks-plugin->get-runbooks])
    (fn [{:keys [show-tree?
                 atom-run-connections-list
                 run-connections-list-rest
                 run-connections-list-selected
                 atom-filtered-run-connections-list
                 metadata
                 metadata-key
                 metadata-value
                 schema-disabled?]}]
      [:aside {:class "h-full flex flex-col gap-8 p-regular border-l border-gray-600 overflow-auto pb-16"}
       (when (seq run-connections-list-selected)
         [:div
          [:> ui/Disclosure {:defaultOpen true}
           (fn [params]
             (r/as-element
              [:<>
               [:> (.-Button ui/Disclosure)
                {:as (if (empty? run-connections-list-selected)
                       "div"
                       "button")
                 :class (str "h-8 w-full flex justify-between items-center gap-small "
                             "text-xs text-gray-400 focus:outline-none focus-visible:ring "
                             "focus-visible:ring-gray-500 focus-visible:ring-opacity-75")}


                [:div {:class "flex items-center gap-small"}
                 [:span "Running in"]
                 (when (> (count run-connections-list-selected) 1)
                   [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-blue-500"}
                    [:span {:class "text-white text-xxs font-semibold"}
                     (count run-connections-list-selected)]])]

                (when-not (empty? run-connections-list-selected)
                  [:> hero-solid-icon/ChevronDownIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                                   "text-white h-5 w-5 shrink-0")
                                                       :aria-hidden "true"}])]

               [:> (.-Panel ui/Disclosure) {:className "py-regular"
                                            :static (if (empty? run-connections-list-selected)
                                                      true
                                                      false)}
                [connections-running-list/main {:schema-disabled? schema-disabled?
                                                :show-tree? show-tree?
                                                :run-connections-list-selected run-connections-list-selected}]]]))]])
       [:div
        [:> ui/Disclosure {:defaultOpen true}
         (fn [params]
           (r/as-element
            [:<>
             [:> (.-Button ui/Disclosure)
              {:class (str "h-8 w-full flex justify-between items-center gap-small "
                           "text-md text-white font-semibold focus:outline-none focus-visible:ring "
                           "focus-visible:ring-gray-500 focus-visible:ring-opacity-75")}

              "Connections"
              [:> hero-solid-icon/ChevronDownIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                               "text-white h-5 w-5 shrink-0")
                                                   :aria-hidden "true"}]]

             [:> (.-Panel ui/Disclosure) {:className "h-full py-regular"}
              [connections-list/main {:run-connections-list-rest run-connections-list-rest
                                      :atom-filtered-run-connections-list atom-filtered-run-connections-list}]]]))]]

       [:div
        [:> ui/Disclosure {:defaultOpen true}
         (fn [params]
           (r/as-element
            [:<>
             [:> (.-Button ui/Disclosure)
              {:class (str "h-8 w-full flex justify-between items-center gap-small "
                           "text-md text-white font-semibold focus:outline-none focus-visible:ring "
                           "focus-visible:ring-gray-500 focus-visible:ring-opacity-75")}

              "Runbooks"
              [:> hero-solid-icon/ChevronDownIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                               "text-white h-5 w-5 shrink-0")
                                                   :aria-hidden "true"}]]

             [:> (.-Panel ui/Disclosure) {:className "py-regular"}
              [runbooks-list/main templates filtered-templates]]]))]]

       [:div
        [:> ui/Disclosure {:defaultOpen false}
         (fn [params]
           (r/as-element
            [:<>
             [:> (.-Button ui/Disclosure)
              {:class (str "h-8 w-full flex justify-between items-center gap-small "
                           "text-md text-white font-semibold focus:outline-none focus-visible:ring "
                           "focus-visible:ring-gray-500 focus-visible:ring-opacity-75")}

              "Metadata"
              [:> hero-solid-icon/ChevronDownIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                               "text-white h-5 w-5 shrink-0")
                                                   :aria-hidden "true"}]]

             [:> (.-Panel ui/Disclosure) {:className "py-regular"}
              [metadata/main {:metadata metadata
                              :metadata-key metadata-key
                              :metadata-value metadata-value}]]]))]]

       [:div {:class "absolute bottom-6 right-6 w-floating-search-webclient"}
        [searchbox/main
         {:options {:connections (:data @atom-run-connections-list)
                    :runbooks (map #(into {} {:name (:name %)}) (:data @templates))}
          :multiple-options? true
          :on-change-results-cb (fn [option]
                                  (rf/dispatch [:editor-plugin->set-filtered-run-connection-list (:connections option)])
                                  (rf/dispatch [:runbooks-plugin->set-filtered-runbooks (:runbooks option)]))
          :floating? true
          :display-key :name
          :searchable-keys [:name :type :subtype :status]
          :hide-results-list true
          :placeholder "Search"
          :name "aside-search"
          :clear? true}]]])))

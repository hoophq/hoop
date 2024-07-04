(ns webapp.shared-ui.sidebar.connection-overlay
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["react" :as react]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.searchbox :as searchbox]
            [webapp.connections.constants :as connection-constants]))

(def overlay-open? (r/atom false))

(defn main []
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])
        connections-search-status (r/atom nil)
        connections (rf/subscribe [:connections])
        searched-connections (r/atom nil)
        search-focused (r/atom false)
        searched-criteria (r/atom "")]
    (fn [user context-connection]
      (let [user-data (:data user)
            admin? (:admin? user-data)
            connection-context-data (:data context-connection)
            connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)
            sidebar-open-desktop? (if (= :opened (:status @sidebar-desktop))
                                    true
                                    false)]
        [:> ui/Transition {:show @overlay-open?
                           :as react/Fragment}
         [:> ui/Dialog {:as "div"
                        :class "relative z-10"
                        :onClose #(reset! overlay-open? %)}
          [:div {:class "fixed inset-0"}]
          [:div {:class "fixed inset-0 overflow-hidden"}
           [:div {:class "absolute inset-0 overflow-hidden"}
            [:div {:class (str "pointer-events-none fixed inset-y-0 top-16 lg:top-0 right-0 flex w-96 "
                               (if sidebar-open-desktop?
                                 "lg:left-side-menu-width"
                                 "lg:left-14"))}
             [:> (.-Child ui/Transition) {:as react/Fragment
                                          :enter "transform transition ease-in-out duration-500 sm:duration-700"
                                          :enterFrom "translate-x-full lg:-translate-x-full"
                                          :enterTo "translate-x-0 lg:translate-x-0"
                                          :leave "transform transition ease-in-out duration-500 sm:duration-700"
                                          :leaveFrom "translate-x-0 lg:translate-x-0"
                                          :leaveTo "translate-x-full lg:-translate-x-full"}
              [:> (.-Panel ui/Dialog) {:class "pointer-events-auto w-screen max-w-md"}
               [:div {:class "flex h-full flex-col overflow-y-scroll bg-white py-6 shadow-xl"}
                [:div {:class "px-4 sm:px-6"}
                 [:div {:class "flex items-start justify-between"}
                  [:> (.-Title ui/Dialog) {:class "text-base font-semibold leading-6 text-gray-900"}
                   "Connections"]
                  [:div {:class "ml-3 flex h-7 items-center"}
                   [:button {:type "button"
                             :class "relative rounded-md bg-white text-gray-400 hover:text-gray-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2"
                             :on-click #(reset! overlay-open? false)}
                    [:span {:class "absolute -inset-2.5"}]
                    [:span {:class "sr-only"}
                     "Close panel"]
                    [:> hero-outline-icon/XMarkIcon {:class "h-6 w-6"
                                                     :aria-hidden "true"}]]]]]
                [:div {:class "relative mt-6 flex-1 px-4 sm:px-6"}
                 [:div {:class "flex flex-col gap-regular"}
                  (when admin?
                    [button/primary {:text "Add connection"
                                     :full-width true
                                     :variant :small
                                     :on-click (fn []
                                                 (rf/dispatch [:navigate :create-connection])
                                                 (reset! overlay-open? false))}])

                  [:div {:class "w-full"}
                   [searchbox/main {:options (:results @connections)
                                    :display-key :name
                                    :variant :small
                                    :searchable-keys [:name :type :tags]
                                    :on-change-results-cb #(reset! searched-connections %)
                                    :hide-results-list true
                                    :placeholder "Search"
                                    :on-focus #(reset! search-focused true)
                                    :on-blur #(reset! search-focused false)
                                    :name "connection-search"
                                    :on-change #(reset! searched-criteria %)
                                    :loading? (= @connections-search-status :loading)
                                    :size :small}]]
                  (when (and (empty? @searched-connections)
                             (> (count @searched-criteria) 0))
                    [:div {:class "text-xs text-gray-700 italic"}
                     "No connections with this criteria"])

                  [:div {:class "transition grid lg:grid-cols-1 gap-regular h-auto"}
                   (doall
                    (for [connection connections-search-results]
                      ^{:key (:name connection)}
                      [:div {:class (str (when (= (:name connection) (:name connection-context-data))
                                           "bg-gray-200 ")
                                         "flex justify-between cursor-pointer items-center gap-small "
                                         "text-sm text-gray-700 hover:bg-gray-200 rounded-md p-2")
                             :on-click (fn []
                                         (rf/dispatch [:connections->get-context-connection (:id connection)])
                                         (rf/dispatch [:editor-plugin->clear-script])
                                         (rf/dispatch [:ask-ai->clear-ai-responses])
                                         (reset! overlay-open? false))}
                       [:div {:class "flex items-center gap-regular"}
                        [:figure {:class "w-5"}
                         [:img {:src  (connection-constants/get-connection-icon connection)
                                :class "w-9"}]]
                        [:span (:name connection)]]
                       (when (seq (:tags connection))
                         [:div {:class (str "relative flex flex-col group")}
                          [:> hero-outline-icon/TagIcon {:class "w-5 h-5"}]
                          [:div {:class "absolute top-8 left-2 flex-col hidden w-max group-hover:flex"}
                           [:div {:class "w-2 h-2 -mt-1 border-l border-t border-gray-300 bg-white transform rotate-45 z-50"}]
                           [:div {:class (str "absolute border -right-3 border-gray-300 bg-white rounded-md z-40 "
                                              "text-xs text-gray-700 leading-none whitespace-no-wrap shadow-lg")}
                            [:ul {:class "max-h-96 overflow-y-auto px-regular pt-regular pb-small"}
                             [:span {:class "text-xs text-gray-400 font-normal pb-small block"}
                              "Tags"]
                             (for [tag (:tags connection)]
                               ^{:key tag}
                               [:li {:class "font-bold text-sm text-gray-900 py-small"}
                                tag])]]]])]))]]]]]]]]]]]))))

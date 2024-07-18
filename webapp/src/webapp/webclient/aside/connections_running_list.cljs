(ns webapp.webclient.aside.connections-running-list
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.connections.constants :as connection-constants]
            [webapp.webclient.aside.database-schema :as database-schema]))

(defn connection-running-item [{:keys [connection
                                       show-tree?
                                       schema-disabled?
                                       removed?
                                       default-opened?
                                       context-connection?]}]
  [:div {:class "flex flex-col gap-0.5"}
   [:> ui/Disclosure {:defaultOpen default-opened?}
    (fn [params]
      (r/as-element
       [:<>
        [:> (.-Button ui/Disclosure)
         {:class (str "h-8 p-2 w-full flex justify-between items-center gap-small "
                      "text-sm text-white bg-gray-800 "
                      (if (.-open params) "rounded-t-md" "rounded-md"))}

         [:div {:class "flex items-center gap-regular"}
          [:figure {:class "w-5"}
           [:img {:src  (connection-constants/get-connection-icon connection :dark)
                  :class "w-9"}]]
          [:span (:name connection)]]
         (when (or (not context-connection?)
                   (and (show-tree? connection)
                        (not (schema-disabled? connection))))
           [:> hero-solid-icon/EllipsisVerticalIcon {:class "text-white h-5 w-5 shrink-0"
                                                     :aria-hidden "true"}])]

        (when (or (not context-connection?)
                  (and (show-tree? connection)
                       (not (schema-disabled? connection))))
          [:> (.-Panel ui/Disclosure) {:className "bg-gray-800 text-white p-2 rounded-b-md"}
           [:ul {:class "flex flex-col gap-2"}
            (when (and (show-tree? connection) (not (schema-disabled? connection)))
              [:li
               [:> ui/Disclosure {:defaultOpen default-opened?}
                (fn [params]
                  (r/as-element
                   [:<>
                    [:> (.-Button ui/Disclosure)
                     {:class (str "flex items-center gap-2 text-xs text-white font-semibold")}
                     [:> hero-solid-icon/CircleStackIcon {:class "text-white h-3 w-3 shrink-0"
                                                          :aria-hidden "true"}]
                     "Database schema"]

                    [:> (.-Panel ui/Disclosure) {:className "bg-gray-800 text-white p-2 rounded-md"}
                     [database-schema/main {:connection-name (:name connection)
                                            :connection-type (cond
                                                               (not (cs/blank? (:subtype connection))) (:subtype connection)
                                                               (not (cs/blank? (:icon_name connection))) (:icon_name connection)
                                                               :else (:type connection))}]]]))]])

            (when removed?
              [:li {:class "flex items-center gap-2 text-xs text-white font-semibold cursor-pointer"
                    :on-click (fn []
                                (rf/dispatch [:editor-plugin->toggle-select-run-connection (:name connection)]))}
               [:> hero-solid-icon/XMarkIcon {:class "text-white h-3 w-3 shrink-0"
                                              :aria-hidden "true"}]
               "Remove selection"])]])]))]])

(defn main [{:keys [current-connection
                    run-connections-list-selected
                    show-tree?
                    schema-disabled?
                    metadata
                    metadata-key
                    metadata-value]}]
  [:div {:class "relative"}
   [:div {:class "transition grid lg:grid-cols-1 gap-regular h-auto"}
    [connection-running-item {:connection current-connection
                              :show-tree? show-tree?
                              :schema-disabled? schema-disabled?
                              :metadata metadata
                              :metadata-key metadata-key
                              :metadata-value metadata-value
                              :removed? false
                              :default-opened? true
                              :context-connection? true}]
    (doall
     (for [connection run-connections-list-selected]
       ^{:key (:name connection)}
       [connection-running-item {:connection connection
                                 :show-tree? show-tree?
                                 :schema-disabled? schema-disabled?
                                 :metadata metadata
                                 :metadata-key metadata-key
                                 :metadata-value metadata-value
                                 :default-opened? false
                                 :removed? true}]))]])

(ns webapp.webclient.aside.connections-list
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            [re-frame.core :as rf]
            [webapp.connections.constants :as connection-constants]))

(defn main [{:keys [run-connections-list-rest
                    atom-filtered-run-connections-list]}]
  [:div {:class "relative"}
   [:div {:class "transition grid lg:grid-cols-1 gap-regular h-auto"}
    (if (empty? (filterv #(not (:selected %)) @atom-filtered-run-connections-list))
      [:div {:class "text-center text-xs text-gray-400 font-normal"}
       "There's no connection matching your search."]

      (doall
       (for [connection run-connections-list-rest]
         ^{:key (:name connection)}
         [:div {:class (str "flex font-semibold justify-between items-center gap-small "
                            "text-sm rounded-md group "
                            (if (= "offline" (:status connection))
                              "text-gray-400"
                              "text-white cursor-pointer"))
                :on-click (fn []
                            (rf/dispatch [:editor-plugin->toggle-select-run-connection (:name connection)]))}
          [:div {:class "flex items-center gap-regular"}
           [:div
            [:figure {:class "w-4"}
             [:img {:src  (connection-constants/get-connection-icon connection :dark)
                    :class "w-4"}]]]
           [:div {:class (str "flex flex-col justify-center")}
            [:span (:name connection)]
            (when (= "offline" (:status connection))
              [:span {:class "font-normal"}
               "Offline"])]]
          (when-not (= "offline" (:status connection))
            [:> hero-micro-icon/PlusIcon {:class "hidden group-hover:block h-4 w-4 text-white"}])])))]])

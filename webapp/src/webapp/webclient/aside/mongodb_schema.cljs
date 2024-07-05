(ns webapp.webclient.aside.mongodb-schema
  (:require
   [reagent.core :as r]))

(defn- collections-tree []
  (let [dropdown-status (r/atom {})]
    (fn [collections]
      [:div {:class "pl-small"}
       (doall
        (for [[collection _] collections]
          ^{:key collection}
          [:div
           [:div {:class "flex items-center gap-small"}
            [:figure {:class "w-3 flex-shrink-0"}
             [:img {:src "/icons/icon-table.svg"}]]
            [:span {:class "flex items-center"
                    :on-click #(swap! dropdown-status
                                      assoc-in [collection]
                                      (if (= (get @dropdown-status collection) :open) :closed :open))}
             [:span collection]]]]))])))

(defn main [_]
  (let [dropdown-status (r/atom {})]
    (fn [schema]
      [:div.text-xs
       (doall
        (for [[db collections] schema]
          ^{:key db}
          [:div
           [:div {:class "flex items-center gap-small"}
            [:figure {:class "w-3 flex-shrink-0"}
             [:img {:src "/icons/icon-layers-dark-gray.svg"}]]
            [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
                                "flex items-center")
                    :on-click #(swap! dropdown-status
                                      assoc-in [db]
                                      (if (= (get @dropdown-status db) :closed) :open :closed))}
             [:span db]
             [:figure {:class "w-4 flex-shrink-0 opacity-30"}
              [:img {:src (if (not= (get @dropdown-status db) :closed)
                            "/icons/icon-cheveron-up.svg"
                            "/icons/icon-cheveron-down.svg")}]]]]
           [:div {:class (when (= (get @dropdown-status db) :closed)
                           "h-0 overflow-hidden")}
            [collections-tree
             (into (sorted-map) collections)]]]))])))


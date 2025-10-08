(ns webapp.features.runbooks.setup.views.runbook-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn- get-connection-paths [connection-name paths-by-connection]
  (get paths-by-connection connection-name []))

(defn connection-item [{:keys [connection total-items]}]
  (fn []
    [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                         "border-[--gray-a6] border "
                         (when (> total-items 1) " first:border-b-0"))}
     [:> Box {:p "5" :class "flex justify-between items-center"}
      [:> Flex {:gap "4" :align "center"}
       [:figure {:class "w-6"}
        [:img {:src (or (connection-constants/get-connection-icon connection) "/icons/database.svg")
               :class "w-full"}]]
       [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
        (:name connection)]]

      [:> Flex {:align "center" :gap "4"}
       [:> Button {:size "3"
                   :variant "soft"
                   :color "gray"
                   :on-click #(rf/dispatch [:navigate :runbooks-edit {} :connection-id (:id connection)])}
        "Configure"]]]]))

(defn main []
  (let [paths-by-connection (rf/subscribe [:runbooks/paths-by-connection])
        all-connections (rf/subscribe [:connections])]

    ;; Fetch all connections when component mounts
    (rf/dispatch [:connections->get-connections {:force-refresh? true}])

    (fn []
      (let [connections (:results @all-connections)
            processed-connections (->> connections
                                       (map (fn [connection]
                                              (let [connection-paths (get-connection-paths (:id connection) @paths-by-connection)]
                                                {:connection connection
                                                 :paths connection-paths})))
                                       (sort-by #(-> % :connection :name)))]
        [:> Box {:class "w-full h-full"}
         [:> Box
          (doall
           (for [conn processed-connections]
             ^{:key (-> conn :connection :id)}
             [connection-item (assoc conn :total-items (count processed-connections))]))]]))))

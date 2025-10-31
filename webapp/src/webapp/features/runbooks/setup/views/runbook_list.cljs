(ns webapp.features.runbooks.setup.views.runbook-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

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
        all-connections (rf/subscribe [:connections->pagination])]

    ;; Fetch connections when component mounts
    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])

    (fn []
      (let [connections-state @all-connections
            connections (or (:data connections-state) [])
            connections-loading? (:loading connections-state false)
            processed-connections (->> connections
                                       (map (fn [connection]
                                              (let [connection-paths (get-connection-paths (:id connection) @paths-by-connection)]
                                                {:connection connection
                                                 :paths connection-paths})))
                                       (sort-by #(-> % :connection :name)))]
        [:> Box {:class "w-full h-full"}
         [infinite-scroll
          {:on-load-more (fn []
                           (when (not connections-loading?)
                             (let [current-page (:current-page connections-state 1)
                                   next-page (inc current-page)
                                   next-request {:page next-page
                                                 :force-refresh? false}]
                               (rf/dispatch [:connections/get-connections-paginated next-request]))))
           :has-more? (:has-more? connections-state false)
           :loading? connections-loading?}
          (doall
           (for [conn processed-connections]
             ^{:key (-> conn :connection :id)}
             [connection-item (assoc conn :total-items (count processed-connections))]))]]))))

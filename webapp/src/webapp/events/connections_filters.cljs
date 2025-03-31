(ns webapp.events.connections-filters
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

(rf/reg-event-fx
 :connections->filter-connections
 (fn
   [{:keys [db]} [_ filters]]
   (let [query-params (cond-> {}
                        (:tag_selector filters) (assoc :tag_selector (:tag_selector filters))
                        (:type filters) (assoc :type (:type filters))
                        (:subtype filters) (assoc :subtype (:subtype filters)))
         uri (if (empty? query-params)
               "/connections"
               (str "/connections?"
                    (cs/join "&"
                             (map (fn [[k v]]
                                    (str (name k) "=" v))
                                  query-params))))]
     {:db (-> db
              (assoc-in [:connections :loading] true)
              (assoc-in [:connections :filter-query] filters))
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri uri
                               :on-success #(rf/dispatch [:connections->set-filtered-connections %])}]]]})))

(rf/reg-event-fx
 :connections->set-filtered-connections
 (fn
   [{:keys [db]} [_ connections]]
   {:db (-> db
            (assoc-in [:connections :results] connections)
            (assoc-in [:connections :loading] false))
    :fx [[:dispatch [:connections->debug-log (str "Filtered connections: " (count connections))]]]}))

(rf/reg-event-fx
 :connections->debug-log
 (fn [_ [_ message]]
   (js/console.log message)
   {}))

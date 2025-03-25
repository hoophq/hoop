(ns webapp.events.connections-filters
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

(rf/reg-event-fx
 :connections->filter-connections
 (fn
   [{:keys [db]} [_ filters]]
   (let [query-params (cond-> {}
                        (:tagSelector filters) (assoc :tagSelector (:tagSelector filters))
                        (:type filters) (assoc :type (:type filters))
                        (:subtype filters) (assoc :subtype (:subtype filters)))
         uri (if (empty? query-params)
               "/connections"
               (str "/connections?"
                    (cs/join "&"
                             (map (fn [[k v]]
                                    (str (name k) "=" v))
                                  query-params))))]
     {:db (assoc-in db [:connections :loading] true)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri uri
                               :on-success #(rf/dispatch [:connections->set-connections %])}]]]})))

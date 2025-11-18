
(ns webapp.webclient.events.search
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

(rf/reg-event-db
 :search/set-term
 (fn [db [_ term]]
   (assoc-in db [:search :term] term)))

(rf/reg-event-db
 :search/clear-term
 (fn [db _]
   (assoc-in db [:search :term] "")))

(rf/reg-event-fx
 :search/filter-runbooks
 (fn [{:keys [db]} [_ search-term]]
   (let [list-data (get-in db [:runbooks :list])
         repositories (:data list-data)
         all-items (mapcat :items (or repositories []))]
     (if (nil? list-data)
       {}
       (let [filtered-runbooks (if (cs/blank? search-term)
                                 (map #(into {} {:name (:name %)}) all-items)
                                 (map #(into {} {:name (:name %)})
                                      (filter (fn [runbook]
                                                (cs/includes?
                                                 (cs/lower-case (:name runbook))
                                                 (cs/lower-case search-term)))
                                              all-items)))]
         {:db (assoc-in db [:search :current-term] search-term)
          :fx [[:dispatch [:runbooks-plugin->set-filtered-runbooks filtered-runbooks]]]})))))

(rf/reg-sub
 :search/term
 (fn [db]
   (get-in db [:search :term] "")))


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
         ;; Build a flat list with repository info for each item
         all-items-with-repo (mapcat (fn [repo]
                                       (map (fn [item]
                                              (assoc item :repository (:repository repo)))
                                            (:items repo)))
                                     (or repositories []))]
     (if (nil? list-data)
       {}
       (let [filtered-runbooks (if (cs/blank? search-term)
                                 all-items-with-repo
                                 (filter (fn [runbook]
                                           (cs/includes?
                                            (cs/lower-case (:name runbook))
                                            (cs/lower-case search-term)))
                                         all-items-with-repo))]
         {:db (assoc-in db [:search :current-term] search-term)
          :fx [[:dispatch [:runbooks/set-filtered-runbooks filtered-runbooks]]]})))))

(rf/reg-sub
 :search/term
 (fn [db]
   (get-in db [:search :term] "")))

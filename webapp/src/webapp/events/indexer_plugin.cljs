(ns webapp.events.indexer-plugin
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :indexer-plugin->search
 (fn
   [{:keys [db]} [_ {:keys [query fields]}]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri "/plugins/indexer/sessions/search"
                             :body {:query query
                                    :limit 100
                                    :highlighter "html"}
                             :on-success #(rf/dispatch [:indexer-plugin->set-search-results %])
                             :on-failure #(rf/dispatch [:show-snackbar {:text %
                                                                        :level :error}])}]]]
    :db (assoc-in db [:indexer-plugin->search :status] :loading)}))

(rf/reg-event-fx
 :indexer-plugin->set-search-results
 (fn
   [{:keys [db]} [_ results]]
   {:db (assoc-in db [:indexer-plugin->search] {:status :ready
                                                :results results})}))

(rf/reg-event-fx
 :indexer-plugin->clear-search-results
 (fn
   [{:keys [db]} [_]]
   {:db (assoc-in db [:indexer-plugin->search] {:status :ready
                                                :results nil})}))


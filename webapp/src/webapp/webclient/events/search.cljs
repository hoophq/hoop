
(ns webapp.webclient.events.search
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

                                   ;; Event para definir o termo de busca atual
(rf/reg-event-db
 :search/set-term
 (fn [db [_ term]]
   (assoc-in db [:search :term] term)))

                                   ;; Event para limpar o termo de busca
(rf/reg-event-db
 :search/clear-term
 (fn [db _]
   (assoc-in db [:search :term] "")))

                                   ;; Event para filtrar runbooks baseado no termo de busca
(rf/reg-event-fx
 :search/filter-runbooks
 (fn [{:keys [db]} [_ search-term]]
   (let [all-runbooks (get-in db [:runbooks-plugin->runbooks :data])]
     (if (nil? all-runbooks)
       {} ;; Se não há runbooks, não faz nada
       (let [filtered-runbooks (if (cs/blank? search-term)
                                  ;; Se o termo estiver vazio, mostre todos os runbooks
                                 (map #(into {} {:name (:name %)}) all-runbooks)
                                  ;; Caso contrário, filtre normalmente
                                 (map #(into {} {:name (:name %)})
                                      (filter (fn [runbook]
                                                (cs/includes?
                                                 (cs/lower-case (:name runbook))
                                                 (cs/lower-case search-term)))
                                              all-runbooks)))]
         {:db (assoc-in db [:search :current-term] search-term)
          :fx [[:dispatch [:runbooks-plugin->set-filtered-runbooks filtered-runbooks]]]})))))

                                   ;; Subscription para o termo de busca atual
(rf/reg-sub
 :search/term
 (fn [db]
   (get-in db [:search :term] "")))

                                   ;; Subscription para verificar se o search está ativo
(rf/reg-sub
 :search/is-active
 (fn [db]
   (let [term (get-in db [:search :term] "")]
     (not (cs/blank? term)))))

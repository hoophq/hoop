(ns webapp.events.command-palette
  (:require
   [clojure.string]
   [re-frame.core :as rf]))

;; Event handlers para o command palette

(rf/reg-event-fx
 :command-palette->open
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:command-palette :open?] true)}))

(rf/reg-event-fx
 :command-palette->close
 (fn [{:keys [db]} [_]]
   {:db (-> db
            (assoc-in [:command-palette :open?] false)
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

(rf/reg-event-fx
 :command-palette->toggle
 (fn [{:keys [db]} [_]]
   (let [current-state (get-in db [:command-palette :open?] false)]
     (if current-state
       {:fx [[:dispatch [:command-palette->close]]]}
       {:fx [[:dispatch [:command-palette->open]]]}))))

(rf/reg-event-fx
 :command-palette->search
 (fn [{:keys [db]} [_ query]]
   (let [trimmed-query (clojure.string/trim query)]
     (if (< (count trimmed-query) 2)
       ;; Query muito curta, limpar resultados
       {:db (-> db
                (assoc-in [:command-palette :query] query)
                (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}
       ;; Query válida, fazer busca imediatamente
       {:db (-> db
                (assoc-in [:command-palette :query] query)
                (assoc-in [:command-palette :search-results :status] :searching))
        :fx [[:dispatch [:command-palette->perform-search trimmed-query]]]}))))

(rf/reg-event-fx
 :command-palette->perform-search
 (fn [{:keys [db]} [_ query]]
   ;; Busca imediata sem debounce
   {:fx [[:dispatch
          [:fetch
           {:method "GET"
            :uri "/search"
            :query-params {:term query}
            :on-success #(rf/dispatch [:command-palette->search-success %])
            :on-failure #(rf/dispatch [:command-palette->search-failure %])}]]]}))

(rf/reg-event-fx
 :command-palette->search-success
 (fn [{:keys [db]} [_ results]]
   {:db (assoc-in db [:command-palette :search-results]
                  {:status :ready
                   :data results})}))

(rf/reg-event-fx
 :command-palette->search-failure
 (fn [{:keys [db]} [_ error]]
   (js/console.error "Command palette search error:" error)
   {:db (assoc-in db [:command-palette :search-results]
                  {:status :error
                   :data {}
                   :error error})}))

;; Inicialização do command palette
(rf/reg-event-fx
 :command-palette->init
 (fn [{:keys [db]} [_]]
   {:db (assoc db :command-palette
               {:open? false
                :query ""
                :search-results {:status :idle :data {}}})}))

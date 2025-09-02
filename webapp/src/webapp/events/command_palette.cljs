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
            (assoc-in [:command-palette :current-page] :main)
            (assoc-in [:command-palette :selected-connection] {})
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
   (let [safe-query (or query "")
         trimmed-query (clojure.string/trim safe-query)
         current-page (get-in db [:command-palette :current-page] :main)]
     ;; Só fazer busca se estivermos na página principal
     (if (= current-page :connection-actions)
       ;; Na página de ações, só atualizar a query (sem buscar)
       {:db (assoc-in db [:command-palette :query] safe-query)}
       ;; Na página principal, fazer busca normal
       (if (< (count trimmed-query) 2)
         ;; Query muito curta, limpar resultados
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}
         ;; Query válida, fazer busca imediatamente
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results :status] :searching))
          :fx [[:dispatch [:command-palette->perform-search trimmed-query]]]})))))

(rf/reg-event-fx
 :command-palette->perform-search
 (fn [_ [_ query]]
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

;; Navegação para ações de uma conexão específica
(rf/reg-event-fx
 :command-palette->show-connection-actions
 (fn [{:keys [db]} [_ connection]]
   {:db (-> db
            (assoc-in [:command-palette :current-page] :connection-actions)
            (assoc-in [:command-palette :selected-connection] connection)
            (assoc-in [:command-palette :query] "")
            ;; Limpar resultados de busca para evitar conflitos
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Voltar para a lista principal
(rf/reg-event-fx
 :command-palette->back-to-main
 (fn [{:keys [db]} [_]]
   {:db (-> db
            (assoc-in [:command-palette :current-page] :main)
            (assoc-in [:command-palette :selected-connection] {})
            (assoc-in [:command-palette :query] "")
            ;; Limpar resultados de busca
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Inicialização do command palette
(rf/reg-event-fx
 :command-palette->init
 (fn [{:keys [db]} [_]]
   {:db (assoc db :command-palette
               {:open? false
                :query ""
                :current-page :main
                :selected-connection {}
                :search-results {:status :idle :data {}}})}))

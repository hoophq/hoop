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
         current-page (get-in db [:command-palette :current-page] :main)
         ;; Busca sempre habilitada na página principal
         search-enabled-pages #{:main}]
     (if (contains? search-enabled-pages current-page)
       ;; Página principal - busca habilitada
       (if (< (count trimmed-query) 2)
         ;; Query muito curta, limpar resultados
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}
         ;; Query válida, fazer busca imediatamente
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results :status] :searching))
          :fx [[:dispatch [:command-palette->perform-search trimmed-query]]]})
       ;; Outras páginas, só atualizar a query (sem buscar)
       {:db (assoc-in db [:command-palette :query] safe-query)}))))

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

;; Navegação para páginas específicas
(rf/reg-event-fx
 :command-palette->navigate-to-page
 (fn [{:keys [db]} [_ page-type & [context]]]
   {:db (-> db
            (assoc-in [:command-palette :current-page] page-type)
            (assoc-in [:command-palette :context] context)
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Voltar para a página principal
(rf/reg-event-fx
 :command-palette->back
 (fn [{:keys [db]} [_]]
   {:db (-> db
            (assoc-in [:command-palette :current-page] :main)
            (assoc-in [:command-palette :context] {})
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Executar ação baseada no tipo
(rf/reg-event-fx
 :command-palette->execute-action
 (fn [_ [_ action]]
   (case (:action action)
     :navigate
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:navigate (:route action)]]]}

     :external
     {:fx [[:dispatch [:command-palette->close]]]}

     ;; Default
     {:fx [[:dispatch [:command-palette->close]]]})))

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

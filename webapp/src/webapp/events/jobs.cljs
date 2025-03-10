(ns webapp.events.jobs
  (:require [re-frame.core :as rf]))

;; Evento para buscar os jobs em andamento
(rf/reg-event-fx
 :jobs/fetch-aws-connect-jobs
 (fn [{:keys [db]} _]
   {:dispatch [:fetch
               {:method "GET"
                :uri "/dbroles/jobs"
                :on-success #(rf/dispatch [:jobs/fetch-aws-connect-jobs-success %])
                :on-failure #(rf/dispatch [:jobs/fetch-aws-connect-jobs-failure %])}]}))

;; Evento para tratar o sucesso da busca de jobs
(rf/reg-event-fx
 :jobs/fetch-aws-connect-jobs-success
 (fn [{:keys [db]} [_ response]]
   (let [jobs (or (:items response) (:data response) [])
         ;; Um job está em execução se:
         ;; 1. Status/phase é "running" OU
         ;; 2. completed_at é null (ainda não concluído)
         has-running-jobs? (some (fn [job]
                                   (or (= (or (:phase job) (:status job)) "running")
                                       (nil? (:completed_at job))))
                                 jobs)]
     (if has-running-jobs?
       ;; Ainda existem jobs em execução, apenas atualizar o estado
       {:db (-> db
                (assoc-in [:jobs :aws-connect] jobs)
                (assoc-in [:jobs :has-running-jobs?] true))}

       ;; Todos os jobs foram concluídos:
       ;; 1. Atualizar o estado
       ;; 2. Parar o polling
       ;; 3. Atualizar a lista de conexões
       {:db (-> db
                (assoc-in [:jobs :aws-connect] jobs)
                (assoc-in [:jobs :has-running-jobs?] false)
                (assoc-in [:jobs :polling-active?] false))
        :dispatch-n [[:jobs/stop-aws-connect-polling]
                     [:connections->get-connections]]}))))

;; Evento para tratar falha na busca de jobs
(rf/reg-event-db
 :jobs/fetch-aws-connect-jobs-failure
 (fn [db [_ _]]
   (-> db
       (assoc-in [:jobs :aws-connect] [])
       (assoc-in [:jobs :has-running-jobs?] false))))

;; Evento para iniciar o polling dos jobs
(rf/reg-event-fx
 :jobs/start-aws-connect-polling
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:jobs :polling-active?] true)
    :dispatch [:jobs/fetch-aws-connect-jobs]
    :dispatch-later [{:ms 5000
                      :dispatch [:jobs/continue-aws-connect-polling]}]}))

;; Evento para continuar o polling dos jobs
(rf/reg-event-fx
 :jobs/continue-aws-connect-polling
 (fn [{:keys [db]} _]
   (if (get-in db [:jobs :polling-active?])
     {:dispatch [:jobs/fetch-aws-connect-jobs]
      :dispatch-later [{:ms 5000
                        :dispatch [:jobs/continue-aws-connect-polling]}]}
     {:db db})))

;; Evento para parar o polling
(rf/reg-event-db
 :jobs/stop-aws-connect-polling
 (fn [db _]
   (assoc-in db [:jobs :polling-active?] false)))

;; Subscription para verificar se existem jobs em execução
(rf/reg-sub
 :jobs/aws-connect-running?
 (fn [db _]
   (get-in db [:jobs :has-running-jobs?] false)))

;; Subscription para obter a lista de jobs
(rf/reg-sub
 :jobs/aws-connect-jobs
 (fn [db _]
   (get-in db [:jobs :aws-connect] [])))

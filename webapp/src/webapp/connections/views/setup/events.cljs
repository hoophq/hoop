(ns webapp.connections.views.setup.events
  (:require [re-frame.core :as rf]))

;; Events
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/update-step :credentials]]]}))

(rf/reg-event-db
 :connection-setup/select-subtype
 (fn [db [_ subtype]]
   (assoc-in db [:connection-setup :subtype] subtype)))

(rf/reg-event-db
 :connection-setup/update-step
 (fn [db [_ step]]
   (assoc-in db [:connection-setup :current-step] step)))

(rf/reg-event-db
 :connection-setup/update-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :credentials field] value)))

;; Server-specific events
(rf/reg-event-db
 :connection-setup/select-app-type
 (fn [db [_ app-type]]
   (-> db
       (assoc-in [:connection-setup :app-type] app-type)
       (assoc-in [:connection-setup :current-step] :os-type))))

(rf/reg-event-db
 :connection-setup/select-os-type
 (fn [db [_ os-type]]
   (-> db
       (assoc-in [:connection-setup :os-type] os-type)
       (assoc-in [:connection-setup :current-step] :credentials))))

(rf/reg-event-db
 :connection-setup/add-environment-variable
 (fn [db [_]]
   (let [current-key (get-in db [:connection-setup :credentials :current-key])
         current-value (get-in db [:connection-setup :credentials :current-value])
         current-vars (get-in db [:connection-setup :credentials :environment-variables] [])]
     (-> db
         (update-in [:connection-setup :credentials :environment-variables]
                    #(conj (or % []) {:key current-key :value current-value}))
         (assoc-in [:connection-setup :credentials :current-key] "")
         (assoc-in [:connection-setup :credentials :current-value] "")))))

(rf/reg-event-db
 :connection-setup/go-back
 (fn [db [_]]
   (let [current-subtype (get-in db [:connection-setup :subtype])
         app-type (get-in db [:connection-setup :app-type])
         os-type (get-in db [:connection-setup :os-type])]
     (cond
       ;; Se estiver na view de instalação, volta para a seleção de tipo
       (and (= current-subtype "console") app-type os-type)
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       ;; Se tiver OS selecionado, limpa o OS
       os-type
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       ;; Se tiver app-type selecionado, limpa o app-type
       app-type
       (-> db
           (assoc-in [:connection-setup :app-type] nil)
           (assoc-in [:connection-setup :current-step] :app-type))

       ;; Se tiver apenas subtype, limpa o subtype
       current-subtype
       (-> db
           (assoc-in [:connection-setup :subtype] nil)
           (assoc-in [:connection-setup :current-step] :type))

       ;; Caso contrário, volta para a seleção de tipo de conexão
       :else
       (assoc-in db [:connection-setup :type] nil)))))

;; Subscriptions
(rf/reg-sub
 :connection-setup/current-step
 (fn [db _]
   (get-in db [:connection-setup :current-step])))

(rf/reg-sub
 :connection-setup/connection-type
 (fn [db _]
   (get-in db [:connection-setup :type])))

(rf/reg-sub
 :connection-setup/connection-subtype
 (fn [db _]
   (get-in db [:connection-setup :subtype])))

(rf/reg-sub
 :connection-setup/credentials
 (fn [db _]
   (get-in db [:connection-setup :credentials])))

(rf/reg-sub
 :connection-setup/app-type
 (fn [db _]
   (get-in db [:connection-setup :app-type])))

(rf/reg-sub
 :connection-setup/os-type
 (fn [db _]
   (get-in db [:connection-setup :os-type])))

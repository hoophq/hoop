(ns webapp.events.guardrails
  "Read-only guardrails state for the CLJS surfaces that still reference the
  feature: resource setup/configure (role association) and the activation
  journey (configured-feature detection).

  Guardrail CRUD lives in the React app (webapp_v2 pages/Guardrails) — the
  create/update/delete events and the active-guardrail form state were removed
  with the CLJS pages."
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :guardrails->get-all
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/guardrails"
                   :on-success (fn [guardrails]
                                 (rf/dispatch [:guardrails->set-all guardrails]))
                   :on-failure (fn [error]
                                 (rf/dispatch [:guardrails->set-all nil error]))}]]]
    :db (assoc db :guardrails->list {:status :loading
                                     :data []})}))

(rf/reg-event-db
 :guardrails->set-all
 (fn [db [_ guardrails]]
   (assoc db :guardrails->list {:status :ready :data guardrails})))

;; SUBSCRIPTIONS
(rf/reg-sub
 :guardrails->list
 (fn [db _]
   (get-in db [:guardrails->list])))

(ns webapp.parallel-mode.events.execution-ui
  (:require
   [re-frame.core :as rf]))

;; ---- Execution UI Events ----

(rf/reg-event-db
 :parallel-mode/set-execution-search
 (fn [db [_ term]]
   (assoc-in db [:parallel-mode :execution :search-term] term)))

(rf/reg-event-db
 :parallel-mode/set-active-tab
 (fn [db [_ tab]]
   (assoc-in db [:parallel-mode :execution :active-tab] tab)))

(rf/reg-event-fx
 :parallel-mode/request-cancel
 (fn [_ _]
   {:fx [[:dispatch [:dialog->open
                     {:title "Stop execution and leave?"
                      :text "If you leave now, resource roles on the queue might not be executed."
                      :action-button? true
                      :text-action-button "Leave"
                      :on-success [:parallel-mode/cancel-pending-executions]}]]]}))


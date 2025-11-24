(ns webapp.events.runbooks-plugin
  (:require [re-frame.core :as rf]))


;; TODO: to be removed after migrate session call to the new event
(rf/reg-event-fx
 :runbooks-plugin->run-runbook
 (fn
   [{:keys [db]} [_ {:keys [file-name params connection-name]}]]
   (let [payload {:file_name file-name
                  :parameters params}
         on-failure (fn [error-message error]
                      (rf/dispatch [:show-snackbar {:text "Failed to execute runbook" :level :error :details error}])
                      (js/setTimeout
                       #(rf/dispatch [:audit->get-session-by-id {:id (:session_id error) :verb "exec"}]) 4000))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "The Runbook was run!"}])

                      ;; This is not the right approach.
                      ;; This was added until we create a better result from runbooks exec
                      (js/setTimeout
                       #(rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}]) 4000))]
     {:fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/plugins/runbooks/connections/" connection-name "/exec")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

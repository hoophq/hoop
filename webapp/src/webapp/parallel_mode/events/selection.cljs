(ns webapp.parallel-mode.events.selection
  (:require
   [re-frame.core :as rf]
   [webapp.parallel-mode.db :as db]
   [webapp.parallel-mode.helpers :as helpers]))

;; ---- Selection Events ----

(rf/reg-event-db
 :parallel-mode/toggle-connection
 (fn [db [_ connection]]
   (let [current-connections (get-in db [:parallel-mode :selection :connections] [])
         new-connections (helpers/toggle-in-collection
                          current-connections
                          connection
                          #(= (:name %1) (:name %2)))]
     (assoc-in db [:parallel-mode :selection :connections] new-connections))))

(rf/reg-event-fx
 :parallel-mode/request-clear-all
 (fn [_ _]
   {:fx [[:dispatch [:dialog->open
                     {:title "Remove selections"
                      :text "This will remove all selected resource roles. This action can't be undone."
                      :action-button? true
                      :text-action-button "Remove"
                      :on-success (fn []
                                    (rf/dispatch [:parallel-mode/clear-all-confirmed]))}]]]}))

(rf/reg-event-fx
 :parallel-mode/clear-all-confirmed
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:parallel-mode :selection :connections] [])
    :fx [[:dispatch [:parallel-mode/persist]]
         [:dispatch [:parallel-mode/close-modal]]]}))

(rf/reg-event-fx
 :parallel-mode/clear-all
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:parallel-mode :selection :connections] [])
    :fx [[:dispatch [:parallel-mode/persist]]]}))

(rf/reg-event-fx
 :parallel-mode/confirm-selection
 (fn [{:keys [db]} _]
   (let [selected-connections (get-in db [:parallel-mode :selection :connections] [])]
     (if (helpers/has-minimum-connections? selected-connections)
       ;; Clear draft state on confirm
       {:db (assoc-in db [:parallel-mode :selection :draft-connections] nil)
        :fx [[:dispatch [:parallel-mode/persist]]
             [:dispatch [:parallel-mode/close-modal]]]}
       {:fx [[:dispatch [:show-snackbar {:level :warning
                                         :text (str "Please select at least "
                                                    db/min-connections
                                                    " connections")}]]]}))))

(rf/reg-event-fx
 :parallel-mode/cancel-selection
 (fn [{:keys [db]} _]
   (let [draft-connections (get-in db [:parallel-mode :selection :draft-connections])]
     {:db (update-in db [:parallel-mode :selection] merge
                     {:connections (or draft-connections [])
                      :draft-connections nil})
      :fx [[:dispatch [:parallel-mode/close-modal]]]})))

;; ---- Seed from the Host's Connection ----

(rf/reg-event-fx
 :parallel-mode/seed-from-host
 (fn [{:keys [db]} [_ source]]
   (let [path (helpers/source->connection-path source)
         host-connection (when path (get-in db path))
         current-connections (get-in db [:parallel-mode :selection :connections] [])]
     ;; Only seed something the list can show and parallel mode can run. An
     ;; offline or exec-disabled role used to be counted in the badge while no
     ;; checkbox was ticked, and submit still executed it. EVL-244.
     (if (and host-connection
              (helpers/valid-for-parallel? host-connection)
              (not (helpers/connection-selected? host-connection current-connections)))
       {:db (update-in db [:parallel-mode :selection :connections] conj host-connection)}
       {}))))

;; ---- Persistence ----

(rf/reg-event-fx
 :parallel-mode/persist
 (fn [{:keys [db]} _]
   (let [connections (get-in db [:parallel-mode :selection :connections])]
     (.setItem js/localStorage
               "parallel-mode-connections"
               (pr-str (helpers/connections->storage-format connections)))
     {})))


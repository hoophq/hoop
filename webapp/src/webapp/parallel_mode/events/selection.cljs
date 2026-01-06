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
 :parallel-mode/clear-all
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:parallel-mode :selection :connections] [])
    :fx [[:dispatch [:parallel-mode/persist]]]}))

(rf/reg-event-fx
 :parallel-mode/confirm-selection
 (fn [{:keys [db]} _]
   (let [selected-connections (get-in db [:parallel-mode :selection :connections] [])]
     (if (helpers/has-minimum-connections? selected-connections)
       {:db (assoc-in db [:parallel-mode :modal :open?] false)
        :fx [[:dispatch [:parallel-mode/persist]]]}
       {:fx [[:dispatch [:show-snackbar {:level :warning
                                         :text (str "Please select at least "
                                                    db/min-connections
                                                    " connections")}]]]}))))

;; ---- Seed from Primary Connection ----

(rf/reg-event-fx
 :parallel-mode/seed-from-primary
 (fn [{:keys [db]} _]
   (let [primary-connection (get-in db [:editor :connections :selected])
         current-connections (get-in db [:parallel-mode :selection :connections] [])]
     (if (and primary-connection
              (not (helpers/connection-selected? primary-connection current-connections)))
       {:db (update-in db [:parallel-mode :selection :connections] conj primary-connection)}
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


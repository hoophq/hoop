(ns webapp.parallel-mode.events.modal
  (:require
   [re-frame.core :as rf]))

;; ---- Modal Control Events ----

(rf/reg-event-db
 :parallel-mode/open-modal
 (fn [db _]
   (-> db
       (assoc-in [:parallel-mode :modal :open?] true)
       (assoc-in [:parallel-mode :modal :search-term] ""))))

(rf/reg-event-db
 :parallel-mode/close-modal
 (fn [db _]
   (assoc-in db [:parallel-mode :modal :open?] false)))

(rf/reg-event-fx
 :parallel-mode/toggle-modal
 (fn [{:keys [db]} _]
   (let [currently-open? (get-in db [:parallel-mode :modal :open?])]
     (if currently-open?
       {:db (assoc-in db [:parallel-mode :modal :open?] false)}
       {:fx [[:dispatch [:parallel-mode/open-modal]]
             [:dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}]]]}))))

; Removed step management - direct connection selection only

(rf/reg-event-db
 :parallel-mode/set-search-term
 (fn [db [_ term]]
   (assoc-in db [:parallel-mode :modal :search-term] term)))

(rf/reg-event-db
 :parallel-mode/clear-search
 (fn [db _]
   (assoc-in db [:parallel-mode :modal :search-term] "")))


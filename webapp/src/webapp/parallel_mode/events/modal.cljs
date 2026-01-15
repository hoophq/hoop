(ns webapp.parallel-mode.events.modal
  (:require
   [re-frame.core :as rf]))

;; ---- Modal Control Events ----

(rf/reg-event-db
 :parallel-mode/open-modal
 (fn [db _]
   (let [current-connections (get-in db [:parallel-mode :selection :connections])]
     (-> db
         (update-in [:parallel-mode :modal] merge {:open? true :search-term ""})
         (assoc-in [:parallel-mode :selection :draft-connections] current-connections)))))

(rf/reg-event-fx
 :parallel-mode/close-modal
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:parallel-mode :modal :open?] false)}))

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


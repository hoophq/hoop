(ns webapp.events.components.modal
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :modal->clear
 (fn [{:keys [db]} [_ _]]
   {:db (assoc-in db [:modal-radix] {:open? false
                                     :content nil})}))

(rf/reg-event-fx
 :modal->set-status
 (fn [{:keys [db]} [_ status]]
   {:db (assoc-in db [:modal-radix :open?] status)}))

(rf/reg-event-fx
 :modal->close
 (fn [{:keys [db]} [_ _]]
   (js/setTimeout #(rf/dispatch [:modal->clear]) 500)
   {:db (assoc-in db [:modal-radix :open?] false)}))

(rf/reg-event-fx
 :modal->open
 (fn [{:keys [db]} [_ data]]
   {:db (assoc-in db [:modal-radix] {:open? true
                                     :content (:content data)})}))

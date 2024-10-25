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
   {:db (assoc-in db [:modal-radix :open?] false)}))

(rf/reg-event-fx
 :modal->re-open
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:modal-radix :open?] true)}))

(rf/reg-event-fx
 :modal->open
 (fn [{:keys [db]} [_ data]]
   {:db (assoc-in db [:modal-radix] {:open? true
                                     :id (:id data)
                                     :maxWidth (:maxWidth data)
                                     :custom-on-click-out (:custom-on-click-out data)
                                     :content (:content data)})}))

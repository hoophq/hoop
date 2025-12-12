(ns webapp.events.components.draggable-card
  (:require
   [re-frame.core :as rf]))

;; Close specific draggable card by connection name
(rf/reg-event-fx
 :draggable-cards->close
 (fn [{:keys [db]} [_ connection-name]]
   {:db (update-in db [:draggable-cards] dissoc connection-name)}))

;; Open draggable card for specific connection
(rf/reg-event-fx
 :draggable-cards->open
 (fn [{:keys [db]} [_ connection-name {:keys [component on-click-close on-click-expand loading]}]]
   {:db (assoc-in db [:draggable-cards connection-name] 
                  {:status (if loading :loading :open)
                   :component component
                   :on-click-close on-click-close
                   :on-click-expand on-click-expand})}))

;; Close all draggable cards
(rf/reg-event-fx
 :draggable-cards->close-all
 (fn [{:keys [db]} [_]]
   {:db (assoc db :draggable-cards {})}))

;; Legacy support - keep old events for backward compatibility
(rf/reg-event-fx
 :draggable-card->close
 (fn [{:keys [db]} [_ _]]
   {:db (assoc-in db [:draggable-card] {:status :closed
                                        :component nil
                                        :on-click-close nil
                                        :on-click-expand nil})}))

(rf/reg-event-fx
 :draggable-card->open
 (fn [{:keys [db]} [_ {:keys [component on-click-close on-click-expand loading]}]]
   {:db (assoc-in db [:draggable-card] {:status (if loading :loading :open)
                                        :component component
                                        :on-click-close on-click-close
                                        :on-click-expand on-click-expand})}))

;; Subscription for multiple cards
(rf/reg-sub
 :draggable-cards
 (fn [db _]
   (get db :draggable-cards {})))

;; Subscription for specific card
(rf/reg-sub
 :draggable-card-by-connection
 (fn [db [_ connection-name]]
   (get-in db [:draggable-cards connection-name])))

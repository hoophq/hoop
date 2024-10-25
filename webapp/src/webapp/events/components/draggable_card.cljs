(ns webapp.events.components.draggable-card
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :draggable-card->close
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc-in db [:draggable-card] {:status :closed
                                        :component nil
                                        :on-click-close nil
                                        :on-click-expand nil})}))

(rf/reg-event-fx
 :draggable-card->open
 (fn
   [{:keys [db]} [_ {:keys [component on-click-close on-click-expand loading]}]]
   {:db (assoc-in db [:draggable-card] {:status (if loading :loading :open)
                                        :component component
                                        :on-click-close on-click-close
                                        :on-click-expand on-click-expand})}))

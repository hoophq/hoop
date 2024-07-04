(ns webapp.events.components.sidebar
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :sidebar-mobile->close
 (fn [{:keys [db]} [_ _]]
   (.setItem js/localStorage "sidebar" "closed")
   {:db (assoc-in db [:sidebar-mobile] {:status :closed})}))

(rf/reg-event-fx
 :sidebar-mobile->open
 (fn [{:keys [db]} [_ _]]
   (.setItem js/localStorage "sidebar" "opened")
   {:db (assoc-in db [:sidebar-mobile] {:status :opened})}))

(rf/reg-event-fx
 :sidebar-desktop->close
 (fn [{:keys [db]} [_ _]]
   (.setItem js/localStorage "sidebar" "closed")
   {:db (assoc-in db [:sidebar-desktop] {:status :closed})}))

(rf/reg-event-fx
 :sidebar-desktop->open
 (fn [{:keys [db]} [_ _]]
   (.setItem js/localStorage "sidebar" "opened")
   {:db (assoc-in db [:sidebar-desktop] {:status :opened})}))

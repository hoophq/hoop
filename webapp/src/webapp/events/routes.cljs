(ns webapp.events.routes
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :routes->set-route
 (fn [{:keys [db]} [_ route]]
   {:db (assoc-in db [:routes->route] route)}))

(rf/reg-event-fx
 :routes->get-route
 (fn [{:keys [db]} [_ _]]
   {:db (assoc-in db [:routes->route] (.-pathname (.-location js/window)))}))

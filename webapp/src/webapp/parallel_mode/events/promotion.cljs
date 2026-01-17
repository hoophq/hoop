(ns webapp.parallel-mode.events.promotion
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :parallel-mode/mark-promotion-seen
 (fn [{:keys [db]} _]
   (.setItem (.-localStorage js/window) "parallel-mode-promotion-seen" "true")
   {:db (assoc-in db [:features :parallel-mode :promotion-seen] true)}))

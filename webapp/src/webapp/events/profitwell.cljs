(ns webapp.events.profitwell
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :profitwell->start
 (fn [{:keys [db]} [_ user]]
   (js/profitwell "start" #js{:user_email (:email user)})
   {}))

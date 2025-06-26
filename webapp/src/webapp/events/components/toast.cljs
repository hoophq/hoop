(ns webapp.events.components.toast
  (:require
   [re-frame.core :as rf]
   [webapp.components.toast :refer [toast-success toast-error]]))

(rf/reg-event-fx
 :hide-snackbar
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db
               :snackbar-status :hidden
               :snackbar-level nil
               :snackbar-text nil)}))

(rf/reg-event-fx
 :show-snackbar
 (fn
   [{:keys [db]} [_ data]]
   (case (:level data)
     :success (toast-success (:text data))
     :error (toast-error
             (:text data)
             nil
             (:details data)))

   #_{:db (assoc db
                 :snackbar-status :shown
                 :snackbar-level (:level data)
                 :snackbar-text (:text data))}))

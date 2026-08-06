(ns webapp.events.components.toast
  (:require
   [re-frame.core :as rf]
   [webapp.components.toast :refer [toast-success toast-error toast-info toast-warning]]))

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
   ;; Accept both keyword (:success) and string ("success") levels so
   ;; React callers dispatching via window.hoopDispatch — which only
   ;; keywordizes object keys, not values — hit the same branches as
   ;; native CLJS callers.
   ;;
   ;; The trailing default matters: `case` with no matching clause throws,
   ;; so an unknown level would take down the caller's event handler over a
   ;; toast. Falling back to info shows the message instead.
   (case (keyword (:level data))
     :success (toast-success (:text data))
     :error (toast-error
             (:text data)
             nil
             (:details data))
     :warning (toast-warning (:text data) (:description data))
     (toast-info (:text data) (:description data)))

   {}))

(ns webapp.events.clipboard
  (:require [re-frame.core :as rf]))

(defonce clipboard-listeners (atom {:copy nil :cut nil :beforecopy nil :beforecut nil}))
(defonce last-notification (atom 0))
(defonce notification-cooldown 2000) ;; 2 seconds

(defn show-clipboard-disabled-message []
  (let [now (js/Date.now)]
    (when (> (- now @last-notification) notification-cooldown)
      (reset! last-notification now)
      (rf/dispatch [:show-snackbar {:level :error
                                    :text "Clipboard copy/cut operations are disabled by administrator"}]))))

(defn handle-clipboard-event [e]
  (.preventDefault e)
  (show-clipboard-disabled-message))

(defn setup-clipboard-listeners []
  (let [copy-handler (fn [e] (handle-clipboard-event e))
        cut-handler (fn [e] (handle-clipboard-event e))
        beforecopy-handler (fn [e] (handle-clipboard-event e))
        beforecut-handler (fn [e] (handle-clipboard-event e))]

    (.addEventListener js/document "copy" copy-handler)
    (.addEventListener js/document "cut" cut-handler)
    (.addEventListener js/document "beforecopy" beforecopy-handler)
    (.addEventListener js/document "beforecut" beforecut-handler)

    (reset! clipboard-listeners {:copy copy-handler
                                 :cut cut-handler
                                 :beforecopy beforecopy-handler
                                 :beforecut beforecut-handler})))

(defn remove-clipboard-listeners []
  (when-let [copy-handler (:copy @clipboard-listeners)]
    (.removeEventListener js/document "copy" copy-handler))
  (when-let [cut-handler (:cut @clipboard-listeners)]
    (.removeEventListener js/document "cut" cut-handler))
  (when-let [beforecopy-handler (:beforecopy @clipboard-listeners)]
    (.removeEventListener js/document "beforecopy" beforecopy-handler))
  (when-let [beforecut-handler (:beforecut @clipboard-listeners)]
    (.removeEventListener js/document "beforecut" beforecut-handler))
  (reset! clipboard-listeners {:copy nil :cut nil :beforecopy nil :beforecut nil}))

;; Effect for managing clipboard listeners
(rf/reg-fx
 :clipboard/manage-listeners
 (fn [enabled?]
   (if enabled?
     (setup-clipboard-listeners)
     (remove-clipboard-listeners))))

;; Event to update clipboard state
(rf/reg-event-fx
 :clipboard/update-state
 (fn [{:keys [db]} [_ disabled?]]
   {:clipboard/manage-listeners disabled?}))

;; Initialize clipboard state based on gateway info
(rf/reg-event-fx
 :clipboard/initialize
 (fn [{db :db} _]
   (let [disabled? (get-in db [:gateway->info :data :disable_clipboard_copy_cut] false)]
     {:clipboard/manage-listeners disabled?})))

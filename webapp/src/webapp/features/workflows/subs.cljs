(ns webapp.features.workflows.subs
  (:require
   [re-frame.core :as rf]
   [webapp.formatters :as formatters]))

(rf/reg-sub
 :workflows/state
 (fn [db _]
   (:workflows db)))

(rf/reg-sub
 :workflows/correlation-id
 :<- [:workflows/state]
 (fn [state _]
   (:correlation-id state)))

(rf/reg-sub
 :workflows/status
 :<- [:workflows/state]
 (fn [state _]
   (:status state)))

(rf/reg-sub
 :workflows/error
 :<- [:workflows/state]
 (fn [state _]
   (:error state)))

(rf/reg-sub
 :workflows/sessions
 :<- [:workflows/state]
 (fn [state _]
   (:sessions state)))

(rf/reg-sub
 :workflows/truncated?
 :<- [:workflows/state]
 (fn [state _]
   (:truncated? state)))

(rf/reg-sub
 :workflows/total
 :<- [:workflows/state]
 (fn [state _]
   (:total state)))

(rf/reg-sub
 :workflows/expanded-set
 :<- [:workflows/state]
 (fn [state _]
   (:expanded state)))

(rf/reg-sub
 :workflows/step-detail
 :<- [:workflows/state]
 (fn [state [_ session-id]]
   (get-in state [:step-details session-id])))

(defn- session-status
  "Derive a high-level status for a single session row.
   Possible values: :running :error :success."
  [session]
  (cond
    (nil? (:end_date session)) :running
    (or (= "REJECTED" (get-in session [:review :status]))
        (and (some? (:exit_code session))
             (not (zero? (:exit_code session))))) :error
    :else :success))

(rf/reg-sub
 :workflows/summary
 :<- [:workflows/sessions]
 (fn [sessions _]
   (let [total-sessions (count sessions)
         statuses (mapv session-status sessions)
         success-count (count (filter #{:success} statuses))
         error-count (count (filter #{:error} statuses))
         running-count (count (filter #{:running} statuses))
         starts (keep #(formatters/iso->ms (:start_date %)) sessions)
         t-start (when (seq starts) (apply min starts))
         last-session-start (when (seq starts) (apply max starts))]
     {:sessions-count total-sessions
      :success-count success-count
      :error-count error-count
      :running-count running-count
      :start-ms t-start
      :last-session-start-ms last-session-start})))

(rf/reg-sub
 :workflows/session-status
 (fn [_ [_ session]]
   (session-status session)))

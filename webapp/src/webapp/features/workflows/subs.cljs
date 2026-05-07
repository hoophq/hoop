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
         ends (keep #(formatters/iso->ms (:end_date %)) sessions)
         t-start (when (seq starts) (apply min starts))
         t-end (when (seq ends) (apply max ends))
         ;; Sum each session's elapsed time so idle gaps between steps don't
         ;; inflate the total. Sessions that haven't ended yet are skipped.
         per-session-durations (keep (fn [s]
                                       (let [start (formatters/iso->ms (:start_date s))
                                             end (formatters/iso->ms (:end_date s))]
                                         (when (and start end)
                                           (max 0 (- end start)))))
                                     sessions)
         duration-ms (when (seq per-session-durations)
                       (reduce + per-session-durations))
         identities (->> sessions
                         (map (fn [s]
                                (or (:user_name s)
                                    (:user s)
                                    (:user_id s))))
                         (remove nil?)
                         distinct)
         machine? (every? #(= "machine" (:identity_type %)) sessions)]
     {:sessions-count total-sessions
      :success-count success-count
      :error-count error-count
      :running-count running-count
      :start-ms t-start
      :end-ms t-end
      :duration-ms duration-ms
      :identities identities
      :machine? machine?})))

(rf/reg-sub
 :workflows/session-status
 (fn [_ [_ session]]
   (session-status session)))

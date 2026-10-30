(ns webapp.audit.session-details-bridge
  (:require
   [re-frame.core :as rf]
   [webapp.audit.views.session-details :as session-details]
   [webapp.shell :as shell]))

;; With experimental.sessions_react on, the React shell owns the session
;; details: it listens to this event on window (features/SessionDetails).
(def ^:private open-event "hoop:session-details-open")

;; React sends this one on window after its details change a session (a
;; decision, an execution, a kill, a re-run; stores/useSessionDetailsStore).
(def ^:private changed-event "hoop:session-details-changed")

(defn- react-details? [db]
  (and (shell/react-shell?)
       (get-in db [:gateway->info :data :feature_flags :experimental.sessions_react] false)))

(rf/reg-sub
 :session-details/react?
 (fn [db _]
   (react-details? db)))

;; The flag arrives with /serverinfo. Before the first answer an opener cannot
;; tell which details to open, and a guess runs both (matrix N05). A failed
;; read settles it: the ClojureScript details open.
(rf/reg-sub
 :session-details/flag-known?
 (fn [db _]
   (let [{:keys [loading data]} (:gateway->info db)]
     (or (some? data) (not loading)))))

;; close-to: the path React opens when the details close (the /sessions/:id page).
(rf/reg-fx
 :session-details-bridge/emit
 (fn [{:keys [id verb close-to]}]
   (.dispatchEvent js/window
                   (js/CustomEvent. open-event
                                    (clj->js {:detail {:sessionId id :verb verb :closeTo close-to}})))))

;; Opens the details of a session: the React modal when the flag is on, the
;; ClojureScript one otherwise.
(rf/reg-event-fx
 :session-details/open
 (fn [{:keys [db]} [_ session]]
   (if (react-details? db)
     {:session-details-bridge/emit session}
     {:fx [[:dispatch [:modal->open {:id "session-details"
                                     :maxWidth "95vw"
                                     :content [session-details/main session]}]]]})))

;; The list behind the React details reads its rows again, as it does after the
;; same actions in the ClojureScript details. Other pages fetch nothing.
(rf/reg-event-fx
 :session-details/changed
 (fn [{:keys [db]} _]
   (when (#{:sessions-panel :session-details-panel} (:active-panel db))
     {:fx [[:dispatch [:audit->get-sessions]]]})))

(defonce ^:private changed-listener
  (.addEventListener js/window changed-event #(rf/dispatch [:session-details/changed])))

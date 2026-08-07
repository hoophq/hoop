(ns webapp.resources.mcp-oauth-warning-test
  "What the admin is told when an MCP OAuth login does not attach to the
  connection it was performed for.

  The gateway already refuses to adopt a login it cannot match to the saved
  connection — the endpoint-mismatch case means the admin authorized one MCP
  server and saved another — and reports the reason on the save response
  (AdoptMCPOAuthGrant). Nothing read it. Every save path showed the same plain
  success toast it shows for a healthy save.

  That silence is the whole defect. The connection works when it is created:
  it holds a frozen HEADER_AUTHORIZATION and nothing about it looks wrong. It
  stops working when the provider expires that token, days later, because no
  grant exists to renew it — at which point the failing session points at
  nothing, least of all at the save that caused it.

  So these tests assert on the level and the reason, not merely that some
  toast appeared: a :success toast here is the bug, and a :warning without the
  gateway's reason leaves the admin unable to tell an endpoint mismatch from
  an expired flow.

  All four save paths are covered, because they are four separate call sites
  that each had to be taught to read the field."
  (:require
   [clojure.string :as str]
   [cljs.test :refer-macros [deftest is testing use-fixtures async]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [re-frame.registrar :as registrar]
   [webapp.events.connections]
   [webapp.events.resources]
   [webapp.resources.add-role.events]
   [webapp.resources.events]
   [webapp.resources.setup.events.mcp-oauth]))

(defonce ^:private toasts (atom []))
(defonce ^:private requests (atom []))
(defonce ^:private queued (atom []))

;; process-payload reads the create form out of app-db and routes on its type;
;; an empty db throws before any save can happen. This is the minimum that
;; makes it produce a payload — what the payload contains is not what these
;; tests are about, what the response handling does with it is.
(def ^:private form-state
  {:connection-setup {:type "httpproxy"
                      :subtype "mcpproxy"
                      :name "linear-mcp"}})

;; Downstream of every save: navigation, the plugin catalog and a connections
;; refetch. No-ops so a save can run end to end.
(def ^:private stubbed-events
  [:modal->close :close-modal :navigate
   :plugins->get-my-plugins
   :connections/get-connections-paginated
   :resource-setup->next-step])

;; re-frame's :dispatch effect is asynchronous, so it is drained by hand here
;; (the convention in mcp_catalog_test) and every assertion below runs against
;; a settled queue rather than a race.
;;
;; :show-snackbar and :fetch become recorders: the toast is collected rather
;; than rendered, the HTTP call captured rather than performed. Both are real
;; handlers registered by the app, so they are SAVED and put back afterwards —
;; clearing them would leave every later test namespace dispatching into a
;; hole.
(defonce ^:private saved-handlers (atom {}))

;; Saved and restored through the registrar because what `get-handler` returns
;; is the built interceptor chain, not the raw fn: putting it back through
;; `reg-event-fx` would wrap it a second time.
(defn- stub-event!
  [id handler]
  (swap! saved-handlers assoc id (registrar/get-handler :event id))
  (rf/reg-event-fx id handler))

(use-fixtures :each
  {:before (fn []
             (reset! toasts [])
             (reset! requests [])
             (reset! queued [])
             (reset! saved-handlers {})
             (reset! rf-db/app-db form-state)
             (rf/reg-fx :dispatch (fn [event] (swap! queued conj event)))
             (stub-event! :show-snackbar
                          (fn [_ [_ data]] (swap! toasts conj data) nil))
             (stub-event! :fetch
                          (fn [_ [_ request]] (swap! requests conj request) nil))
             (doseq [id stubbed-events]
               (stub-event! id (fn [_ _] nil))))
   :after (fn []
            (rf/clear-fx :dispatch)
            (doseq [[id handler] @saved-handlers]
              (if handler
                (registrar/register-handler :event id handler)
                (rf/clear-event id))))})

(defn- drain!
  "Run the dispatch queue to exhaustion, the way re-frame would."
  []
  (loop [guard 100]
    (when-let [event (first @queued)]
      (when (zero? guard)
        (throw (ex-info "event queue did not settle" {:pending @queued})))
      (swap! queued subvec 1)
      (rf/dispatch-sync event)
      (recur (dec guard)))))

(defn- dispatch! [event]
  (rf/dispatch-sync event)
  (drain!))

(defn- levels [] (mapv :level @toasts))

(defn- warning-toast []
  (first (filter #(= :warning (:level %)) @toasts)))

(defn- save!
  "Run a save event and answer the request it issues with `response`.

  The :on-success callbacks call rf/dispatch directly rather than returning a
  :dispatch effect, so their events land on re-frame's own router queue. That
  queue is drained on a later tick, which is why this is the one helper that
  has to be awaited — `check` runs once it has settled."
  [event response check]
  (dispatch! event)
  ((:on-success (last @requests)) response)
  (js/setTimeout (fn [] (drain!) (check)) 0))

(def ^:private refusal
  (str "the OAuth login was not attached to this connection, so its credential "
       "will not be renewed and the connection stops working when the provider "
       "expires the current token: the oauth login authorized "
       "https://a.example/mcp but the connection points at https://b.example/mcp"))

;; ---------------------------------------------------------------------------
;; PUT /connections/:name — the two update paths
;; ---------------------------------------------------------------------------

(deftest updating-a-role-reports-a-refused-oauth-login
  (async done
    (save! [:resources->update-role-connection {:name "linear-mcp"}]
           {:name "linear-mcp" :mcp_oauth_warning refusal}
           (fn []
             (testing "the save is not announced as a plain success"
               (is (not (some #{:success} (levels)))))
             (testing "the gateway's reason reaches the admin"
               (is (= :warning (:level (warning-toast))))
               (is (str/includes? (:description (warning-toast)) "https://b.example/mcp")))
             (done)))))

(deftest updating-a-role-without-a-warning-is-a-plain-success
  (async done
    (save! [:resources->update-role-connection {:name "linear-mcp"}]
           {:name "linear-mcp"}
           (fn []
             (is (= [:success] (levels)))
             (is (str/includes? (:text (first @toasts)) "linear-mcp"))
             (done)))))

(deftest updating-a-connection-reports-a-refused-oauth-login
  (async done
    (save! [:connections->update-connection {:name "linear-mcp"}]
           {:name "linear-mcp" :mcp_oauth_warning refusal}
           (fn []
             (is (not (some #{:success} (levels))))
             (is (= :warning (:level (warning-toast))))
             (done)))))

(deftest updating-a-connection-without-a-warning-is-a-plain-success
  (async done
    (save! [:connections->update-connection {:name "linear-mcp"}]
           {:name "linear-mcp"}
           (fn []
             (is (= [:success] (levels)))
             (done)))))

;; ---------------------------------------------------------------------------
;; POST /connections — the standalone create path
;; ---------------------------------------------------------------------------

(deftest creating-a-connection-reports-a-refused-oauth-login
  (async done
    (save! [:connections->create-connection {:name "linear-mcp"}]
           {:name "linear-mcp" :mcp_oauth_warning refusal}
           (fn []
             (is (not (some #{:success} (levels))))
             (is (= :warning (:level (warning-toast))))
             (done)))))

(deftest creating-a-connection-without-a-warning-is-a-plain-success
  (async done
    (save! [:connections->create-connection {:name "linear-mcp"}]
           {:name "linear-mcp"}
           (fn []
             (is (= [:success] (levels)))
             (done)))))

;; ---------------------------------------------------------------------------
;; POST /resources — the create wizard, which saves N roles at once
;; ---------------------------------------------------------------------------

;; The wizard reports per role, because only some of the roles it just created
;; may be degraded and the admin has to know which. The healthy ones are still
;; a success, so both toasts appear.
(deftest the-wizard-names-the-roles-whose-login-was-refused
  (dispatch! [:resources->create-success
              {:name "linear"
               :mcp_oauth_warnings [{:name "linear-mcp" :warning refusal}]}])
  (is (= #{:success :warning} (set (levels))))
  (is (str/includes? (:text (warning-toast)) "linear-mcp"))
  (is (str/includes? (:description (warning-toast)) "https://b.example/mcp")))

(deftest the-wizard-stays-quiet-when-every-login-attached
  (dispatch! [:resources->create-success {:name "linear"}])
  (is (= [:success] (levels))))

;; Adding roles to an existing resource creates them one at a time and
;; summarizes at the end, so the warning has to survive the aggregation rather
;; than be reported per response and lost.
(deftest adding-roles-names-the-ones-whose-login-was-refused
  (swap! rf-db/app-db assoc :resource-setup
         {:created-roles [{:name "healthy-mcp"}
                          {:name "linear-mcp" :mcp_oauth_warning refusal}]})
  (dispatch! [:add-role->all-roles-processed])
  (is (= #{:success :warning} (set (levels))))
  (is (str/includes? (:text (warning-toast)) "linear-mcp"))
  (is (not (str/includes? (:text (warning-toast)) "healthy-mcp"))))

(deftest adding-roles-stays-quiet-when-every-login-attached
  (swap! rf-db/app-db assoc :resource-setup
         {:created-roles [{:name "healthy-mcp"}]})
  (dispatch! [:add-role->all-roles-processed])
  (is (= [:success] (levels))))

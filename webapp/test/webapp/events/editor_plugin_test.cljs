(ns webapp.events.editor-plugin-test
  "The order of a web editor run on a connection with forms (ENG-689).

  With experimental.analyze_before_run_forms on, the analyzer judges the
  script before the Jira or metadata form opens: a blocked script never opens
  a form. These run the real events against app-db and answer the
  POST /sessions/analyze request by hand."
  (:require
   [cljs.test :refer-macros [deftest is use-fixtures]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [re-frame.registrar :as registrar]
   ;; Registers the real :fetch, put back after each test.
   [webapp.events]
   [webapp.events.editor-plugin]))

;; Events dispatched through the :dispatch effect or rf/dispatch, in order.
(defonce ^:private queued (atom []))
;; Requests sent through the :fetch event.
(defonce ^:private fetches (atom []))

(defonce ^:private real-fetch (registrar/get-handler :event :fetch))

(use-fixtures :each
  {:before (fn []
             (reset! queued [])
             (reset! fetches [])
             (.removeItem js/localStorage "selected-database")
             (rf/reg-fx :dispatch (fn [event] (swap! queued conj event)))
             (rf/reg-fx :dispatch-later (fn [_]))
             (rf/reg-event-fx :fetch (fn [_ [_ request]] (swap! fetches conj request) {})))
   :after (fn []
            (rf/clear-fx :dispatch)
            (rf/clear-fx :dispatch-later)
            (registrar/register-handler :event :fetch real-fetch))})

(def ^:private previous-output {:status :success :data {:output "id\n1"}})

(defn- seed-db! [flag-on?]
  (reset! rf-db/app-db
          {:gateway->info {:data {:feature_flags {:experimental.analyze_before_run_forms flag-on?}}}
           :jira-integration->details {:data {:status "enabled"}}
           :editor {:connections {:selected {:name "pg-gmud"}}}
           :connections {:details {"pg-gmud" {:name "pg-gmud"
                                              :type "database"
                                              :subtype "postgres"
                                              :jira_issue_template_id "tmpl-1"}}}
           :editor-plugin {:metadata []}
           :editor-plugin->script previous-output}))

(defn- event-ids [] (mapv first @queued))

(defn- take-queued!
  "Returns the queued event with id, and empties the queue."
  [id]
  (let [event (some #(when (= id (first %)) %) @queued)]
    (reset! queued [])
    event))

(defn- submit! []
  (rf/dispatch-sync [:editor-plugin/submit-task-with-fresh-data {:script "DROP TABLE customers"}]))

(defn- analyze! []
  (submit!)
  (rf/dispatch-sync (take-queued! :editor-plugin/analyze-before-forms))
  (let [request (take-queued! :fetch)]
    (rf/dispatch-sync request)
    (last @fetches)))

(deftest flag-on-analyzes-before-any-form
  (seed-db! true)
  (submit!)
  (is (= [:editor-plugin/analyze-before-forms] (event-ids)))
  (rf/dispatch-sync (take-queued! :editor-plugin/analyze-before-forms))
  (is (= {:status :loading} (:editor-plugin->script @rf-db/app-db)))
  (rf/dispatch-sync (take-queued! :fetch))
  (let [request (last @fetches)]
    (is (= "/sessions/analyze" (:uri request)))
    (is (= {:script "DROP TABLE customers" :connection "pg-gmud"}
           (select-keys (:body request) [:script :connection])))))

(deftest a-blocked-script-opens-no-form
  (seed-db! true)
  (let [request (analyze!)
        blocked {:session_id "s1"
                 :output "Session blocked by AI risk analyzer"
                 :output_status "blocked"
                 :ai_analysis {:action "block_execution" :title "Drops a table"}}]
    (with-redefs [rf/dispatch (fn [event] (swap! queued conj event))]
      ((:on-success request) blocked))
    (is (= [:webapp.events.editor-plugin/editor-plugin->set-script-success] (event-ids)))
    (rf/dispatch-sync (take-queued! :webapp.events.editor-plugin/editor-plugin->set-script-success))
    (is (= "block_execution"
           (get-in @rf-db/app-db [:editor-plugin->script :data :ai_analysis :action])))
    (is (not-any? #{:modal->open} (event-ids)))))

(deftest an-allowed-script-opens-the-form-over-the-previous-output
  (seed-db! true)
  (let [request (analyze!)]
    (with-redefs [rf/dispatch (fn [event] (swap! queued conj event))]
      ((:on-success request) nil))
    (rf/dispatch-sync (take-queued! :editor-plugin/open-run-forms))
    (is (= previous-output (:editor-plugin->script @rf-db/app-db))
        "a cancelled form leaves the previous output, as without the flag")
    (rf/dispatch-sync (take-queued! :editor-plugin/submit-task-with-fresh-data))
    (is (= [:modal->open :jira-templates->get-submit-template] (event-ids)))))

(deftest a-second-submit-while-the-analysis-loads-is-ignored
  (seed-db! true)
  (let [request (analyze!)]
    ;; Mod-Enter pressed again before the first analysis answers.
    (submit!)
    (rf/dispatch-sync (take-queued! :editor-plugin/analyze-before-forms))
    (is (empty? @queued) "no second analysis request")
    (is (= 1 (count @fetches)))
    (with-redefs [rf/dispatch (fn [event] (swap! queued conj event))]
      ((:on-success request) nil))
    (rf/dispatch-sync (take-queued! :editor-plugin/open-run-forms))
    (is (= previous-output (:editor-plugin->script @rf-db/app-db))
        "a cancelled form must not leave the editor loading")))

(deftest a-412-falls-back-to-the-forms
  (seed-db! true)
  (let [request (analyze!)]
    (with-redefs [rf/dispatch (fn [event] (swap! queued conj event))]
      ((:on-failure request) {:status 412 :message "turn on the flag"}))
    (is (= [:editor-plugin/open-run-forms] (event-ids)))))

(deftest an-analyzer-error-opens-no-form
  (seed-db! true)
  (let [request (analyze!)]
    (with-redefs [rf/dispatch (fn [event] (swap! queued conj event))]
      ((:on-failure request) {:status 500 :message "failed analyzing session"}))
    (is (= [:show-snackbar :webapp.events.editor-plugin/editor-plugin->set-script-failure] (event-ids)))))

(deftest flag-off-opens-the-form-first
  (seed-db! false)
  (submit!)
  (is (= [:modal->open :jira-templates->get-submit-template] (event-ids)))
  (is (empty? @fetches)))

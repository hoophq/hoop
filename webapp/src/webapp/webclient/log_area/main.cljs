(ns webapp.webclient.log-area.main
  (:require ["@radix-ui/themes" :refer [Box Flex]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.audit.views.session-details :as session-details]
            [webapp.components.ag-grid-table :as ag-grid-table]
            [webapp.components.results-download-menu :as download-menu]
            [webapp.components.results-matrix :as results-matrix]
            [webapp.features.activation-journey.views.terminal-banner :as terminal-banner]
            [webapp.webclient.log-area.output-tabs :refer [tabs]]
            [webapp.webclient.log-area.logs :as logs]))

(def selected-tab (r/atom (or (.getItem js/localStorage "webclient-selected-tab")
                              "Logs")))

(defn- clean-postgres-script [script]
  (let [lines (cs/split script #"\n")]
    (if (and (> (count lines) 3)
             (= (first lines) "\\set QUIET on"))
      (cs/join "\n" (drop 3 lines))  ;; Pula as 3 primeiras linhas
      script)))

(defn- clean-mssql-script [script]
  (let [lines (cs/split script #"\n")]
    (if (and (> (count lines) 2)
             (= (first lines) "SET NOCOUNT ON;"))
      (cs/join "\n" (drop 2 lines))
      script)))

;; TODO: Change it for send DB in the payload and not the response
(defn- sanitize-response [response connection-type]
  (cond
    (= connection-type "mssql")
    (when response
      (if-let [idx (cs/index-of response "\n")]
        (subs response (inc idx))
        response))
    :else response))

(defn main [_]
  (let [script-response (rf/subscribe [:editor-plugin->script])
        matrix-cache (results-matrix/new-cache)]
    (fn [connection-type parallel-mode-active? dark-mode?]
      (let [response (sanitize-response (:output (:data @script-response)) connection-type)
            logs-content {:status (:status @script-response)
                          :response response
                          :response-status (:output_status (:data @script-response))
                          :script (cond
                                    (= connection-type "postgres")
                                    (clean-postgres-script (:script (:data @script-response)))
                                    (= connection-type "mssql")
                                    (clean-mssql-script (:script (:data @script-response)))
                                    :else (:script (:data @script-response)))
                          :response-id (:session_id (:data @script-response))
                          :has-review (:has_review (:data @script-response))
                          :execution-time (:execution_time (:data @script-response))
                          :classes "h-full"}
            tabular-status (:status @script-response)
            tabular-loading? (= tabular-status :loading)
            connection-type-database? (some (partial = connection-type)
                                            ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
            ;; Above the threshold downloads go to the backend, so only
            ;; Tabular needs the matrix.
            build-matrix? (boolean
                           (and connection-type-database?
                                (or (= @selected-tab "Tabular")
                                    (<= (count response)
                                        download-menu/client-side-threshold))))
            parsed (if build-matrix?
                     (results-matrix/parse-results matrix-cache response)
                     (results-matrix/release-stale! matrix-cache response))
            results-heads (:heads parsed)
            results-body (:body parsed)
            available-tabs (merge
                            {:logs "Logs"}
                            (when (and connection-type-database?
                                       (not parallel-mode-active?))
                              {:tabular "Tabular"}))
            tabular-data? (and connection-type-database?
                               (if build-matrix?
                                 (boolean (and results-heads results-body
                                               (pos? (.-length results-heads))
                                               (pos? (.-length results-body))))
                                 (results-matrix/rows? response)))
            session-id (:session_id (:data @script-response))
            on-view-session-details (when session-id
                                      #(rf/dispatch
                                        [:modal->open
                                         {:id "session-details"
                                          :maxWidth "95vw"
                                          :content [session-details/main
                                                    {:id session-id :verb "exec"}]}]))
            menu-props (when session-id
                         {:results response
                          :matrix (:matrix parsed)
                          :tabular? (boolean (and (= tabular-status :success)
                                                  tabular-data?))
                          :session-id session-id
                          :connection-name nil
                          :has-large-payload? false
                          :on-view-session-details on-view-session-details})]

        (when-not (some #(= @selected-tab %) (vals available-tabs))
          (.setItem js/localStorage "webclient-selected-tab" (first (vals available-tabs)))
          (reset! selected-tab (first (vals available-tabs))))

        [:> Box {:class "flex-1 min-h-0 flex flex-col overflow-hidden"}
         [:> Box {:class "h-full flex flex-col bg-gray-2 border-b border-gray-3"}
          [:> Flex {:justify "between" :align "center" :gap "4" :class "pr-small"}
           [:> Box {:class "flex-1 min-w-0"}
            [tabs {:on-click (fn [_ value]
                               (.setItem js/localStorage "webclient-selected-tab" value)
                               (reset! selected-tab value))
                   :tabs available-tabs
                   :selected-tab @selected-tab}]]
           (when menu-props
             [:> Box {:class "mb-regular pt-small flex-shrink-0"}
              [download-menu/main menu-props]])]
          [terminal-banner/main]
          [:> Box {:role "tabpanel"
                   :id (str "tabpanel-" (case @selected-tab
                                          "Tabular" :tabular
                                          "Logs" :logs
                                          :logs))
                   :aria-labelledby (str "tab-" (case @selected-tab
                                                  "Tabular" :tabular
                                                  "Logs" :logs
                                                  :logs))
                   :class "flex-1 min-h-0 overflow-hidden"}
           (case @selected-tab
             "Tabular" [ag-grid-table/main results-heads results-body tabular-loading? dark-mode?
                        {:height "100%"
                         :pagination? (boolean (and results-body
                                                    (> (.-length results-body) 100)))
                         :auto-size-columns? true}]
             "Logs" [logs/main :logs logs-content]
             :else [logs/main logs-content])]]]))))

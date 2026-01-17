(ns webapp.webclient.log-area.main
  (:require ["papaparse" :as papa]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.ag-grid-table :as ag-grid-table]
            [webapp.webclient.log-area.output-tabs :refer [tabs]]
            [webapp.webclient.log-area.logs :as logs]))

(defn- transform-results->matrix
  [results connection-type]
  (when-not (nil? results)
    (get (js->clj (papa/parse results (clj->js {"delimiter" "\t"}))) "data")))

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
  (let [script-response (rf/subscribe [:editor-plugin->script])]
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
            results-transformed (transform-results->matrix response connection-type)
            results-heads (first results-transformed)
            results-body (next results-transformed)
            connection-type-database? (some (partial = connection-type)
                                            ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
            available-tabs (merge
                            {:logs "Logs"}
                            (when (and connection-type-database?
                                       (not parallel-mode-active?))
                              {:tabular "Tabular"}))]

        (when-not (some #(= @selected-tab %) (vals available-tabs))
          (.setItem js/localStorage "webclient-selected-tab" (first (vals available-tabs)))
          (reset! selected-tab (first (vals available-tabs))))

        [:div {:class "h-full flex flex-col"}
         [:div {:class "h-full flex flex-col bg-gray-1 border-b border-gray-3"}
          [tabs {:on-click (fn [_ value]
                             (.setItem js/localStorage "webclient-selected-tab" value)
                             (reset! selected-tab value))
                 :tabs available-tabs
                 :selected-tab @selected-tab}]
          (case @selected-tab
            "Tabular" [ag-grid-table/main results-heads results-body tabular-loading? dark-mode?
                       {:height "100%"
                        :pagination? (> (count results-body) 100)
                        :auto-size-columns? true}]
            "Logs" [logs/main :logs logs-content]
            :else [logs/main logs-content])]]))))

(ns webapp.audit.views.results-container
  (:require ["papaparse" :as papa]
            [clojure.string :as string]
            [reagent.core :as r]
            [webapp.components.data-grid-table :as data-grid-table]
            [webapp.components.logs-container :as logs]
            [webapp.components.tabs :as tabs]))

(def log-view (r/atom "Table"))

(defn- transform-results->matrix
  [results]
  (when-not (nil? results)
    (get (js->clj (papa/parse results (clj->js {"delimiter" "\t"}))) "data")))

(defn tab-container
  [{:keys [results-heads results-body exceed-limit-rows?]} {:keys [status results]}]
  [:div {:class "flex flex-col h-96"}
   [tabs/tabs {:on-change #(reset! log-view %)
               :tabs (if exceed-limit-rows?
                       ["Plain text"]
                       ["Table" "Plain text"])}]
   (case @log-view
     "Plain text" [logs/new-container {:status status :logs results}]
     "Table" [data-grid-table/main results-heads results-body])])

(defmulti results-view identity)
(defmethod results-view :sql
  [_ {:keys [results-heads results-body results status exceed-limit-rows? fixed-height? classes]}]
  [tab-container
   {:results-heads results-heads :results-body results-body :exceed-limit-rows? exceed-limit-rows?}
   {:status status :results results :fixed-height? fixed-height? :classes classes}])

(defmethod results-view :not-sql
  [_ {:keys [results status fixed-height? classes]}]
  [:div {:class "h-96"}
   [logs/new-container {:status status :fixed-height? fixed-height? :logs results :classes classes}]])

(defn main [_]
  (fn [{:keys [results results-status fixed-height? connection-type classes]}]
    (let [sanitize-results (when-not (nil? results)
                             (string/replace results #"∞" "\t"))
          results-transformed (transform-results->matrix sanitize-results)
          results-heads (first results-transformed)
          results-body (next results-transformed)
          exceed-limit-rows? (> (count results-body) 55000)
          props-log-view {:results-heads results-heads
                          :results-body results-body
                          :connection connection-type
                          :fixed-height? fixed-height?
                          :status results-status
                          :results sanitize-results
                          :classes classes
                          :exceed-limit-rows? exceed-limit-rows?}]
      (reset! log-view (if exceed-limit-rows? "Plain text" "Table"))
      (if (= results-status :success)
        (case connection-type
          "mysql-csv" [results-view :sql props-log-view]
          "mysql" [results-view :sql props-log-view]
          "postgres" [results-view :sql props-log-view]
          "sql-server" [results-view :sql props-log-view]
          "mssql" [results-view :sql props-log-view]
          "postgres-csv" [results-view :sql props-log-view]
          "sql-server-csv" [results-view :sql props-log-view]
          "database" [results-view :sql props-log-view]
          [results-view :not-sql props-log-view])
        [results-view :not-sql props-log-view]))))

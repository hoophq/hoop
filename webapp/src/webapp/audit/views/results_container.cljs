(ns webapp.audit.views.results-container
  (:require
   ["papaparse" :as papa]
   [clojure.string :as string]
   [reagent.core :as r]
   [webapp.components.ag-grid-table :as ag-grid-table]
   [webapp.components.logs-container :as logs]
   [webapp.components.tabs :as tabs]))

(defn- transform-results->matrix
  [results connection-type]
  (let [res (if (= connection-type "oracledb")
              (string/join "\n" (drop 1 (string/split results #"\n")))
              results)]
    (when-not (nil? results)
      (get (js->clj (papa/parse res (clj->js {"delimiter" "\t"}))) "data"))))

(defn tab-container
  [{:keys [results-heads results-body not-clipboard? log-view]}
   {:keys [status results]}]
  [:div {:class "flex flex-col h-96"}
   [tabs/tabs {:on-change #(reset! log-view %)
               :tabs ["Plain text" "Table"]}]
   (case @log-view
     "Plain text" [logs/new-container {:status status :logs results :not-clipboard? not-clipboard? :whitespace? true}]
     "Table" [ag-grid-table/main results-heads results-body false true
              {:height "100%"
               :theme "alpine"
               :pagination? (> (count results-body) 100)
               :auto-size-columns? true}])])

(defmulti results-view identity)
(defmethod results-view :sql
  [_ {:keys [results-heads results-body results status fixed-height? classes not-clipboard? log-view]}]
  [tab-container
   {:results-heads results-heads
    :results-body results-body
    :not-clipboard? not-clipboard?
    :log-view log-view}
   {:status status :results results :fixed-height? fixed-height? :classes classes}])

(defmethod results-view :not-sql
  [_ {:keys [results status fixed-height? classes not-clipboard?]}]
  [:div {:class "h-96"}
   [logs/new-container {:status status
                        :fixed-height? fixed-height?
                        :logs results
                        :classes classes
                        :whitespace? true
                        :not-clipboard? not-clipboard?}]])

(defn main []
  (let [log-view (r/atom "Plain text")]

    (fn [connection-subtype {:keys [results results-status fixed-height? classes not-clipboard?]}]
      (let [results-transformed (transform-results->matrix results connection-subtype)
            results-heads (first results-transformed)
            results-body (next results-transformed)
            props-log-view {:results-heads results-heads
                            :results-body results-body
                            :fixed-height? fixed-height?
                            :status results-status
                            :results results
                            :classes classes
                            :not-clipboard? not-clipboard?
                            :log-view log-view}]

        (if (= results-status :success)
          (case connection-subtype
            "mysql-csv" [results-view :sql props-log-view]
            "mysql" [results-view :sql props-log-view]
            "postgres" [results-view :sql props-log-view]
            "sql-server" [results-view :sql props-log-view]
            "mssql" [results-view :sql props-log-view]
            "postgres-csv" [results-view :sql props-log-view]
            "sql-server-csv" [results-view :sql props-log-view]
            "oracledb" [results-view :sql props-log-view]
            "database" [results-view :sql props-log-view]
            [results-view :not-sql props-log-view])
          [results-view :not-sql props-log-view])))))

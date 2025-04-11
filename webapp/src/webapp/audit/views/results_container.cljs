(ns webapp.audit.views.results-container
  (:require ["papaparse" :as papa]
            [clojure.string :as string]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.data-grid-table :as data-grid-table]
            [webapp.components.logs-container :as logs]
            [webapp.components.tabs :as tabs]))

(def log-view (r/atom "Table"))

(defn- transform-results->matrix
  [results connection-type]
  (let [res (if (= connection-type "oracledb")
              (string/join "\n" (drop 1 (string/split results #"\n")))
              results)]
    (when-not (nil? results)
      (get (js->clj (papa/parse res (clj->js {"delimiter" "\t"}))) "data"))))

(defn tab-container
  [{:keys [results-heads results-body exceed-limit-rows? not-clipboard?]} {:keys [status results]}]
  [:div {:class "flex flex-col h-96"}
   [tabs/tabs {:on-change #(reset! log-view %)
               :tabs (if exceed-limit-rows?
                       ["Plain text"]
                       ["Table" "Plain text"])}]
   (case @log-view
     "Plain text" [logs/new-container {:status status :logs results :not-clipboard? not-clipboard?}]
     "Table" [data-grid-table/main results-heads results-body false false (not not-clipboard?)])])

(defmulti results-view identity)
(defmethod results-view :sql
  [_ {:keys [results-heads results-body results status exceed-limit-rows? fixed-height? classes not-clipboard?]}]
  [tab-container
   {:results-heads results-heads
    :results-body results-body
    :exceed-limit-rows? exceed-limit-rows?
    :not-clipboard? not-clipboard?}
   {:status status :results results :fixed-height? fixed-height? :classes classes}])

(defmethod results-view :not-sql
  [_ {:keys [results status fixed-height? classes not-clipboard?]}]
  [:div {:class "h-96"}
   [logs/new-container {:status status
                        :fixed-height? fixed-height?
                        :logs results
                        :classes classes
                        :not-clipboard? not-clipboard?}]])

(defn main [connection-name]
  (let [connection (rf/subscribe [:connections->connection-details])]
    (rf/dispatch [:connections->get-connection-details connection-name])

    (fn [_ {:keys [results results-status fixed-height? classes not-clipboard?]}]
      (let [current-connection (:data @connection)
            connection-type (cond
                              (not (string/blank? (:subtype current-connection))) (:subtype current-connection)
                              (not (string/blank? (:icon_name current-connection))) (:icon_name current-connection)
                              :else (:type current-connection))
            sanitize-results (when-not (nil? results)
                               (string/replace results #"∞" "\t"))
            results-transformed (transform-results->matrix sanitize-results connection-type)
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
                            :exceed-limit-rows? exceed-limit-rows?
                            :not-clipboard? not-clipboard?}]
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
            "oracledb" [results-view :sql props-log-view]
            "database" [results-view :sql props-log-view]
            [results-view :not-sql props-log-view])
          [results-view :not-sql props-log-view])))))

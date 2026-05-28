(ns webapp.audit.views.results-container
  (:require
   ["@radix-ui/themes" :refer [Box Flex]]
   ["papaparse" :as papa]
   [clojure.string :as string]
   [reagent.core :as r]
   [webapp.components.ag-grid-table :as ag-grid-table]
   [webapp.components.logs-container :as logs]
   [webapp.components.results-download-menu :as download-menu]
   [webapp.components.tabs :as tabs]))

(defn- transform-results->matrix
  [results connection-type]
  (let [res (if (= connection-type "oracledb")
              (string/join "\n" (drop 1 (string/split results #"\n")))
              results)]
    (when-not (nil? results)
      (get (js->clj (papa/parse res (clj->js {"delimiter" "\t"}))) "data"))))

(defn tab-container
  [{:keys [results-heads results-body log-view download-props]}
   {:keys [status results]}]
  [:> Flex {:direction "column" :class "h-96 min-h-96"}
   [:> Flex {:justify "between" :align "center" :gap "4" :class "flex-shrink-0"}
    [:> Box {:class "flex-1 min-w-0"}
     [tabs/tabs {:on-change #(reset! log-view %)
                 :tabs ["Plain text" "Table"]}]]
    (when download-props
      [:> Box {:class "mb-large flex-shrink-0"}
       [download-menu/main download-props]])]
   [:> Box {:class "flex-1 min-h-0 overflow-hidden"}
    (case @log-view
      "Plain text" [logs/virtualized-container {:status status :logs results}]
      "Table" [ag-grid-table/main results-heads results-body false true
               {:height "100%"
                :theme "alpine"
                :pagination? (> (count results-body) 100)
                :auto-size-columns? true}])]])

(defmulti results-view identity)
(defmethod results-view :sql
  [_ {:keys [results-heads results-body results status fixed-height? classes log-view download-props]}]
  [tab-container
   {:results-heads results-heads
    :results-body results-body
    :log-view log-view
    :download-props download-props}
   {:status status :results results :fixed-height? fixed-height? :classes classes}])

(defmethod results-view :not-sql
  [_ {:keys [results status classes download-props fixed-height?]}]
  [:> Flex {:direction "column"
            :class (if fixed-height? "h-96 min-h-96" "h-full")}
   (when download-props
     [:> Flex {:justify "end" :class "mb-small flex-shrink-0"}
      [download-menu/main download-props]])
   [:> Box {:class "flex-1 min-h-0 overflow-hidden"}
    [logs/virtualized-container {:status status :logs results :classes classes}]]])

(defn main []
  (let [log-view (r/atom "Plain text")]

    (fn [connection-subtype {:keys [results results-status fixed-height? classes
                                    session-id connection-name has-large-payload?]}]
      (let [results-transformed (transform-results->matrix results connection-subtype)
            results-heads (first results-transformed)
            results-body (next results-transformed)
            sql-subtypes #{"mysql-csv" "mysql" "postgres" "sql-server" "mssql"
                           "postgres-csv" "sql-server-csv" "oracledb" "database"}
            is-sql? (contains? sql-subtypes connection-subtype)
            tabular? (and is-sql?
                          (seq results-heads)
                          (seq results-body))
            download-props (when (= results-status :success)
                             {:results results
                              :matrix results-transformed
                              :tabular? tabular?
                              :session-id session-id
                              :connection-name connection-name
                              :has-large-payload? has-large-payload?})
            props-log-view {:results-heads results-heads
                            :results-body results-body
                            :fixed-height? fixed-height?
                            :status results-status
                            :results results
                            :classes classes
                            :log-view log-view
                            :download-props download-props}]

        (if (= results-status :success)
          (if is-sql?
            [results-view :sql props-log-view]
            [results-view :not-sql props-log-view])
          [results-view :not-sql props-log-view])))))

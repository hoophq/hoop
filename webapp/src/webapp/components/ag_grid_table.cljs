(ns webapp.components.ag-grid-table
  (:require [reagent.core :as r]
            ["ag-grid-react" :refer [AgGridReact]]))

(defn parse-data [head body]
  (let [;; Parse column definitions from head
        col-defs (mapv (fn [field] {:field (name field)}) head)

        ;; Filter out summary rows (rows with strings like "(10 rows)") and empty rows
        filtered-body (filter (fn [row]
                                (and (vector? row)
                                     (> (count row) 0)
                                     (not (and (= (count row) 1)
                                               (string? (first row))
                                               (re-find #"rows" (first row))))))
                              body)

        ;; Parse row data from body
        row-data (mapv (fn [row]
                         (into {} (map-indexed (fn [idx value]
                                                 (when (< idx (count head))
                                                   [(name (nth head idx)) value]))
                                               row)))
                       filtered-body)]
    {:col-defs col-defs
     :row-data row-data}))

(defn main [head body dark-theme? loading?]
  (let [parsed-data (parse-data head body)]

    [:div {:style {:width "100%" :height "100%"}
           :data-ag-theme-mode (if dark-theme? "dark" "light")}
     [:> AgGridReact
      {:rowData (clj->js (:row-data parsed-data))
       :columnDefs (clj->js (:col-defs parsed-data))
       :loading loading?
       :cellSelection true
       :copyHeadersToClipboard true}]]))

(ns webapp.components.ag-grid-table
  (:require
   ["ag-grid-react" :refer [AgGridReact]]
   ["ag-grid-community" :refer [iconOverrides
                                colorSchemeLightWarm
                                colorSchemeDarkBlue
                                themeAlpine]]
   ["lucide-react" :refer [LoaderCircle AlertTriangle]]))

(defonce icon-overrides (iconOverrides
                         #js{:type "image"
                             :mask "true"
                             :icons #js{"filter" #js{:svg "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"24\" height=\"24\" viewBox=\"0 0 24 24\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"2\" stroke-linecap=\"round\" stroke-linejoin=\"round\" class=\"lucide lucide-funnel-icon lucide-funnel\"><path d=\"M10 20a1 1 0 0 0 .553.895l2 1A1 1 0 0 0 14 21v-7a2 2 0 0 1 .517-1.341L21.74 4.67A1 1 0 0 0 21 3H3a1 1 0 0 0-.742 1.67l7.225 7.989A2 2 0 0 1 10 14z\"/></svg>"}}}))

(defonce alpine-light
  (-> themeAlpine
      (.withPart colorSchemeLightWarm)
      (.withPart icon-overrides)))

(defonce alpine-dark
  (-> themeAlpine
      (.withPart colorSchemeDarkBlue)
      (.withPart icon-overrides)))

(defn normalize-row-data
  "Normalizes rows data to ensure consistency with the number of header columns,
   removing empty cells when there are excess columns."
  [headers rows]
  (mapv (fn [row]
          (let [header-count (count headers)
                row-count (count row)]
            (cond
              ;; If there are more columns than needed, remove empty cells
              (> row-count header-count)
              (let [;; Filter non-empty cells
                    non-empty-cells (filterv #(and (not (nil? %))
                                                   (or (not (string? %))
                                                       (not (empty? %))))
                                             row)
                    ;; Number of non-empty cells
                    non-empty-count (count non-empty-cells)]
                ;; If we have enough non-empty cells, use only them
                ;; Otherwise, fill with empty cells up to the required number
                (if (>= non-empty-count header-count)
                  (subvec non-empty-cells 0 header-count)  ;; Still need to truncate if there are more non-empty cells than expected
                  (into non-empty-cells (repeat (- header-count non-empty-count) ""))))

              ;; If there are fewer columns than needed, add empty cells
              (< row-count header-count)
              (into row (repeat (- header-count row-count) ""))

              ;; If the number of columns is exact, keep the row as is
              :else row)))
        rows))

(defn ag-grid
  "An efficient table component using AG Grid to display SQL query results.

   Parameters:
   - columns: vector of column definitions in the format [{:field 'field_name' :headerName 'Title' ...}]
   - rows: vector of row data as maps
   - options: map of additional options for the AG Grid
     - :theme - theme to use (default, alpine, balham, material)
     - :height - table height (default 400px)
     - :pagination? - enable pagination (default false)
     - :auto-size-columns? - automatically adjust columns (default true)"
  [{:keys [columns rows options]}]
  (let [default-options {:height "400px"
                         :pagination? false
                         :auto-size-columns? true}
        merged-options (merge default-options options)]

    (fn [{:keys [dark-mode?]}]
      [:div {:style {:height (:height merged-options)
                     :width "100%"}}
       [:> AgGridReact {:theme (if dark-mode? alpine-dark alpine-light)
                        :columnDefs (clj->js columns)
                        :rowData (clj->js rows)
                        :defaultColDef (clj->js {:resizable true
                                                 :sortable true
                                                 :filter true
                                                 :editable true})
                        :pagination (boolean (:pagination? merged-options))
                        :paginationPageSize (or (:page-size merged-options) 20)
                        :onGridReady (fn [params]
                                       (when (and (:auto-size-columns? merged-options)
                                                  (aget params "columnApi"))
                                         (.autoSizeAllColumns (aget params "columnApi"))))}]])))

(defn error-message
  "Component to display error message with malformed data"
  [message dark-mode?]
  [:div {:class "flex flex-col items-center justify-center h-full text-red-500 p-4"}
   [:div {:class "flex items-center mb-2"}
    [:> AlertTriangle {:size 24
                       :className "mr-2"
                       :color (if dark-mode? "#ff6b6b" "#d32f2f")}]
    [:span {:class "font-medium"} "Data Error"]]
   [:p {:class "text-center"}
    message]
   [:p {:class "mt-4 text-sm text-center text-gray-500"}
    "Check if the data contains tab characters (\\t) within values or if there are inconsistencies in the format."]])

(defn main
  "Main component to display an AG Grid table with SQL query results.

   Parameters:
   - headers: vector of column headers as strings ['Column1', 'Column2', ...]
   - rows: vector of vectors with the data of the rows [['val1', 'val2'], ['val3', 'val4']]
   - loading?: boolean flag indicating if the data is loading
   - options: additional options to pass to ag-grid"
  [headers rows loading? dark-mode? & [options]]
  (if loading?
    [:div {:class "flex justify-center items-center h-full"}
     [:figure.w-4
      [:> LoaderCircle {:class "animate-spin"
                        :size 24
                        :color (if dark-mode? "white" "gray")}]]]

    (let [empty-data? (or (nil? headers) (empty? headers) (nil? rows) (empty? rows))]
      (if empty-data?
        [:div {:class "flex justify-center items-center h-full text-gray-500"}
         "No results available"]

        (try
          ;; Process data with normalization and render the grid
          (let [normalized-rows (normalize-row-data headers rows)
                columns (mapv (fn [header]
                                (let [field-name (if (string? header) header (str header))]
                                  {:field field-name
                                   :cellEditor "agTextCellEditor"
                                   :headerName field-name}))
                              headers)
                row-data (mapv (fn [row]
                                 (reduce (fn [acc [idx val]]
                                           (let [field-name (if (string? (nth headers idx))
                                                              (nth headers idx)
                                                              (str (nth headers idx)))]
                                             (assoc acc field-name val)))
                                         {}
                                         (map-indexed vector row)))
                               normalized-rows)]
            [ag-grid {:columns columns
                      :rows row-data
                      :options options
                      :dark-mode? dark-mode?}])

          ;; Catch any unexpected errors during processing
          (catch :default e
            (let [error-msg (str "Error processing data: " (.-message e))]
              (.error js/console error-msg e)
              [error-message error-msg dark-mode?])))))))

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

(defn- field-name [header]
  (if (string? header) header (str header)))

(defn- row-source
  "Wide rows drop their empty cells first, so ragged output still lines up."
  [row column-count]
  (if (> (.-length row) column-count)
    (.filter row (fn [cell] (and (some? cell) (not= cell ""))))
    row))

(defn headers->col-defs [headers]
  (let [out (array)]
    (dotimes [i (.-length headers)]
      (let [field (field-name (aget headers i))]
        (.push out #js {:field field
                        :cellEditor "agTextCellEditor"
                        :headerName field})))
    out))

(defn rows->row-data
  "Normalizes each row to the header count and keys it by column name."
  [headers rows]
  (let [column-count (.-length headers)
        fields (.map headers (fn [header] (field-name header)))
        out (array)]
    (dotimes [i (.-length rows)]
      (let [src (row-source (aget rows i) column-count)
            obj #js {}]
        (dotimes [j column-count]
          (let [cell (aget src j)]
            (aset obj (aget fields j) (if (nil? cell) "" cell))))
        (.push out obj)))
    out))

(defn ag-grid
  "Renders AG Grid over JS column definitions and row objects.

   - columns: JS array of column defs
   - rows: JS array of row objects
   - options: :height, :pagination?, :page-size, :auto-size-columns?"
  [{:keys [columns rows options dark-mode?]}]
  (let [{:keys [height pagination? page-size auto-size-columns?]}
        (merge {:height "400px" :pagination? false :auto-size-columns? true}
               options)]
    [:section {:style {:height height :width "100%"}
               :aria-label "Query results table"}
     [:> AgGridReact {:theme (if dark-mode? alpine-dark alpine-light)
                      :columnDefs columns
                      :rowData rows
                      :defaultColDef #js {:resizable true
                                          :sortable true
                                          :filter true
                                          :editable true}
                      :pagination (boolean pagination?)
                      :paginationPageSize (or page-size 20)
                      :onGridReady (fn [params]
                                     (when (and auto-size-columns?
                                                (aget params "columnApi"))
                                       (.autoSizeAllColumns (aget params "columnApi"))))}]]))

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

(defn- empty-js-array? [a]
  (or (nil? a) (zero? (.-length a))))

(defn main
  "Main component to display an AG Grid table with SQL query results.

   Parameters:
   - headers: JS array of column headers
   - rows: JS array of JS arrays with the row data
   - loading?: boolean flag indicating if the data is loading
   - options: additional options to pass to ag-grid"
  [headers rows loading? dark-mode? & [options]]
  (if loading?
    [:div {:class "flex justify-center items-center h-full"}
     [:figure.w-4
      [:> LoaderCircle {:class "animate-spin"
                        :size 24
                        :color (if dark-mode? "white" "gray")}]]]

    (if (or (empty-js-array? headers) (empty-js-array? rows))
      [:div {:class "flex justify-center items-center h-full text-gray-500"}
       "No results available"]

      (try
        [ag-grid {:columns (headers->col-defs headers)
                  :rows (rows->row-data headers rows)
                  :options options
                  :dark-mode? dark-mode?}]

        (catch :default e
          (let [error-msg (str "Error processing data: " (.-message e))]
            (.error js/console error-msg e)
            [error-message error-msg dark-mode?]))))))

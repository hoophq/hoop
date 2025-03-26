(ns webapp.components.data-table-simple
  (:require
   ["@radix-ui/themes" :refer [Table Box Checkbox Flex]]
   ["lucide-react" :refer [ChevronDown ChevronRight AlertCircle]]
   [reagent.core :as r]))

(defn data-table-simple
  "A simplified data table component with expandable rows and error handling.

   Required props:
   - :columns - Vector of column definition maps with required keys:
       - :id - Unique identifier for the column
       - :header - Column header text or component
       Optional keys:
       - :width - CSS width value (e.g. \"30%\")
       - :render - Custom render function (fn [value row] -> component)
       - :align - Text alignment ('left', 'center', 'right')

   - :data - Vector of data items to display. Each item is a map that can optionally include:
       - :children - Vector of child rows that will be shown when the row is expanded
       - :error - Error data to display when the row is expanded

   Optional props:
   - :key-fn - Function to extract unique ID from row (default: :id)
   - :selected-ids - Set of selected row IDs
   - :on-select-row - Function called when row is selected (id, selected?)
   - :on-select-all - Function called when all rows are selected (selected?)
   - :selectable? - Function to determine if row can be selected (default: all rows selectable)
   - :loading? - Whether data is loading (boolean)
   - :empty-state - Component or text to show when table is empty
   - :sticky-header? - Whether header should stick to top when scrolling (default: true)
   - :expanded-rows - Set of expanded row IDs (optional)
   - :on-toggle-expand - Function called when a row's expansion is toggled (id)"
  [initial-props]
  (let [internal-expanded-rows (r/atom #{})
        update-counter (r/atom 0)

        toggle-expand (fn [id expanded-rows on-toggle-expand]
                        (if on-toggle-expand
                          ;; Se on-toggle-expand foi fornecido, use-o
                          (on-toggle-expand id)
                          ;; Caso contrário, use o estado interno
                          (do
                            (swap! internal-expanded-rows #(if (contains? % id)
                                                             (disj % id)
                                                             (conj % id)))
                            (swap! update-counter inc))))]

    (fn [{:keys [columns
                 data
                 key-fn
                 selected-ids
                 on-select-row
                 on-select-all
                 selectable?
                 loading?
                 empty-state
                 sticky-header?
                 expanded-rows
                 on-toggle-expand]
          :or {key-fn :id
               selectable? (constantly true)
               sticky-header? true
               empty-state "No data available"
               selected-ids #{}}}]

      (let [all-selected? (and (seq data)
                               (seq selected-ids)
                               (= (count (filter #(and (selectable? %) (contains? selected-ids (key-fn %))) data))
                                  (count (filter selectable? data))))

            ;; Use expanded-rows fornecido ou o interno
            effective-expanded-rows (or expanded-rows @internal-expanded-rows)]

        @update-counter

        [:> Table.Root {:variant "surface"
                        :class (str "border rounded-lg overflow-hidden shadow-sm"
                                    (when sticky-header? " relative"))}

         ;; Table Header
         [:> Table.Header {:class (when sticky-header? "sticky top-0 z-10 bg-gray-50")}
          [:> Table.Row {:class "bg-[--gray-2]" :align "center"}

           ;; Selection column (if enabled)
           (when on-select-row
             [:> Table.ColumnHeaderCell {:p "2" :width "40px" :align "center"}
              [:> Checkbox {:checked all-selected?
                            :onCheckedChange #(when on-select-all
                                                (on-select-all (not all-selected?)))
                            :size "1"
                            :variant "surface"
                            :color "indigo"}]])

           ;; Expansion column (always present)
           [:> Table.ColumnHeaderCell {:p "2" :width "40px"}]

           ;; Column headers
           (for [{:keys [id header width align]} columns]
             ^{:key id}
             [:> Table.ColumnHeaderCell {:key id
                                         :width width
                                         :style {:textAlign align}
                                         :class "font-medium text-[--gray-12]"}
              header])

           ;; Error indicator column (always present)
           [:> Table.ColumnHeaderCell {:p "2" :width "40px"}]]]

         ;; Table Body
         [:> Table.Body

          ;; Loading state
          (when loading?
            [:tr
             [:td {:colSpan (+ (count columns) 3) ;; +3 for selection, expansion, and error columns
                   :class "p-4 text-center"}
              [:div {:class "flex justify-center items-center p-4"} "Loading..."]]])

          ;; Empty state
          (when (and (empty? data) (not loading?))
            [:tr
             [:td {:colSpan (+ (count columns) 3)
                   :class "p-4 text-center text-gray-500"}
              empty-state]])

          ;; Render rows recursively (handles both main rows and children)
          (let [render-rows (fn render-rows-fn [rows indent-level]
                              (doall
                               (for [row rows
                                     :let [row-id (key-fn row)
                                           has-children? (and (:children row) (seq (:children row)))
                                           has-error? (boolean (:error row))
                                           is-expandable? (or has-children? has-error?)
                                           is-expanded? (contains? effective-expanded-rows row-id)
                                           selected? (contains? selected-ids row-id)
                                           ;; Pegar primeira coluna para indentação
                                           first-col (first columns)]]
                                 ^{:key row-id}
                                 [:<>
                                  ;; Row
                                  [:> Table.Row {:align "center"
                                                 :class (str
                                                         (when selected? "bg-blue-50 ")
                                                         (when has-error? "bg-red-50 ")
                                                         "hover:bg-[--gray-3] transition-colors ")}

                                   ;; Selection checkbox
                                   (when on-select-row
                                     [:> Table.Cell {:p "2" :align "center" :width "40px" :justify "center"}
                                      (when (selectable? row)
                                        [:> Checkbox {:checked selected?
                                                      :onCheckedChange #(on-select-row row-id (not selected?))
                                                      :size "1"
                                                      :variant "surface"
                                                      :color "indigo"}])])

                                   ;; Expansion control para árvore (só mostra se tiver filhos ou erro)
                                   [:> Table.Cell {:p "2" :width "40px" :class "relative"}
                                    (when is-expandable?
                                      [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors"
                                                :on-click (fn [e]
                                                            (.stopPropagation e)
                                                            (toggle-expand row-id effective-expanded-rows on-toggle-expand))}
                                       (if is-expanded?
                                         [:> ChevronDown {:size 16}]
                                         [:> ChevronRight {:size 16}])])]

                                   ;; Data cells
                                   (doall
                                    (for [[col-idx {:keys [id header width align render]}] (map-indexed vector columns)
                                          :let [value (get row id)
                                                display-value (if render
                                                                (render value row)
                                                                value)
                                                is-first-col? (zero? col-idx)]]
                                      ^{:key id}
                                      [:> Table.Cell {:p "2"
                                                      :style {:textAlign align}
                                                      :width width
                                                      :class "text-[--gray-12]"}

                                      ;; Para primeira coluna de dados, adicionar indentação se for filho
                                       [:div {:style (when (and is-first-col? (pos? indent-level))
                                                       {:paddingLeft (str (* indent-level 20) "px")})}
                                        display-value]]))

                                   ;; Error indicator com botão de expansão ao lado
                                   [:> Table.Cell {:p "2" :width "40px" :class "relative"}
                                    (when has-error?
                                      [:> Flex {:align "center" :gap "1"}
                                       [:> AlertCircle {:size 16 :class "text-red-500"}]
                                       [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors ml-1"
                                                 :on-click (fn [e]
                                                             (.stopPropagation e)
                                                             (toggle-expand row-id effective-expanded-rows on-toggle-expand))}
                                        (if is-expanded?
                                          [:> ChevronDown {:size 14}]
                                          [:> ChevronRight {:size 14}])]])]]

                                  ;; Expanded error content
                                  (when (and has-error? is-expanded?)
                                    [:tr
                                     [:td {:colSpan (+ (count columns) 3)
                                           :class "p-0 bg-gray-900"}
                                      [:div {:class "p-4 text-white"}
                                       [:pre {:class "whitespace-pre-wrap"}
                                        (js/JSON.stringify (clj->js (:error row)) nil 2)]]]])

                                  ;; Expanded children
                                  (when (and has-children? is-expanded?)
                                    (render-rows-fn (:children row) (inc indent-level)))])))]

          ;; Start rendering from top-level rows with indent level 0
            (render-rows data 0))]]))))

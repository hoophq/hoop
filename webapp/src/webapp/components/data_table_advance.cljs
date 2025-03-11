(ns webapp.components.data-table-advance
  (:require
   ["@radix-ui/themes" :refer [Table Box Checkbox]]
   ["lucide-react" :refer [ChevronDown ChevronRight AlertCircle]]))

(defn data-table-advanced
  "An advanced data table component with extensive features and customization options.

   Options:
   - :columns - Vector of column definition maps, each with:
       - :id - Column identifier
       - :header - Column header text or component
       - :accessor - Function to extract value from row data (default: #(get % id))
       - :render - Function to render cell content (receives value and row)
       - :width - CSS width value
       - :min-width - CSS min-width value
       - :align - Text alignment ('left', 'center', 'right')
       - :hidden? - Whether to hide column (boolean)
       - :class - Additional CSS classes for cells in this column

   - :data - Vector of data items to display
   - :original-data - Original hierarchical data (for tree view)

   - Selection options:
       - :selected-ids - Set of selected row IDs
       - :on-select-row - Function called when a row is selected (id, selected?)
       - :on-select-all - Function called when all rows are selected (selected?)
       - :selectable? - Function to determine if row can be selected (row -> boolean)

   - Expandable row options:
       - :row-expandable? - Function to determine if row can be expanded (row -> boolean)
       - :row-expanded? - Function to determine if row is expanded (row -> boolean)
       - :on-toggle-expand - Function called when row expansion is toggled (id)

   - Tree view options:
       - :tree-data? - Whether data is hierarchical (boolean)
       - :get-children - Function to get children for a row
       - :parent-id-field - Field name for parent ID (default: 'parent-id')
       - :id-field - Field name for ID (default: 'id')

   - Error handling:
       - :row-error - Function that returns error data for a row or nil
       - :error-indicator - Component to use as error indicator

   - Misc options:
       - :key-fn - Function to extract unique ID from a row (default: :id)
       - :empty-state - Component to render when no data is available
       - :loading? - Whether data is loading
       - :loading-component - Component to show during loading
       - :sticky-header? - Whether header should stick to top when scrolling"
  [{:keys [columns
           data
           original-data
           selected-ids
           on-select-row
           on-select-all
           selectable?
           key-fn
           row-expandable?
           row-expanded?
           on-toggle-expand
           row-error
           error-indicator
           empty-state
           loading?
           loading-component
           sticky-header?
           tree-data?
           parent-id-field]
    :or {key-fn :id
         parent-id-field "parent-id"
         selectable? (constantly true)
         row-expandable? (constantly false)
         row-expanded? (constantly false)
         row-error (constantly nil)
         error-indicator (fn [] [:> AlertCircle {:size 16 :class "text-red-500"}])
         loading-component [:div {:class "flex justify-center items-center p-4"} "Loading..."]}}]

  (let [all-selected? (and (seq data)
                           (seq selected-ids)
                           (= (count (filter #(and (selectable? %) (contains? selected-ids (key-fn %))) data))
                              (count (filter selectable? data))))]

    [:> Table.Root {:variant "surface"
                    :class (str "border rounded-lg overflow-hidden shadow-sm"
                                (when sticky-header? " relative"))}

     ;; Table Header
     [:> Table.Header {:class (when sticky-header? "sticky top-0 z-10 bg-gray-50")}
      [:> Table.Row {:class "bg-[--gray-2]" :align "center"}

       ;; Selection column if selection is enabled
       (when on-select-row
         [:> Table.ColumnHeaderCell {:p "2" :width "40px" :align "center"}
          [:> Checkbox {:checked all-selected?
                        :onCheckedChange #(when on-select-all
                                            (on-select-all (not all-selected?)))
                        :size "1"
                        :variant "surface"
                        :color "indigo"}]])

       ;; Expansion column if any rows are expandable
       (when (or (some row-expandable? data) tree-data?)
         [:> Table.ColumnHeaderCell {:p "2" :width "40px"}])

       ;; Column headers
       (for [{:keys [id header width min-width align hidden? class]} columns
             :when (not hidden?)]
         ^{:key id}
         [:> Table.ColumnHeaderCell {:key id
                                     :width width
                                     :style {:minWidth min-width
                                             :textAlign align}
                                     :class (str "font-medium text-[--gray-12] " class)}
          header])

       [:> Table.ColumnHeaderCell {:p "2" :width "40px"}
        [:> Box {:width "36px" :height "36px" :background "transparent"}]]]]

     ;; Table Body
     [:> Table.Body

      ;; Loading state
      (when loading?
        [:tr
         [:td {:colSpan (+ (count (remove :hidden? columns))
                           (if on-select-row 1 0)
                           (if (or (some row-expandable? data) tree-data?) 2 0))
               :class "p-4"}
          loading-component]])

      ;; Empty state if no data
      (when (and (empty? data) (not loading?))
        [:tr
         [:td {:colSpan (+ (count (remove :hidden? columns))
                           (if on-select-row 1 0)
                           (if (or (some row-expandable? data) tree-data?) 2 0))
               :class "p-4 text-center text-gray-500"}
          (or empty-state "No data available")]])

      ;; Data rows
      (doall
       (for [[idx row] (map-indexed vector data)
             :let [row-id (key-fn row)
                   selected? (contains? selected-ids row-id)
                   expandable? (row-expandable? row)
                   expanded? (and expandable? (row-expanded? row))
                   _ (when expandable? (js/console.log "Row" row-id "expandable:" expandable? "expanded:" expanded?))
                   error-data (row-error row)
                   has-error? (boolean error-data)
                   is-child? (and tree-data? (get row parent-id-field))
                   row-indent (if (and tree-data? is-child?)
                                0
                                0)]]
         ^{:key row-id}
         [:<>
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

          ;; Expansion control
           (when (or (some row-expandable? data) tree-data?)
             [:> Table.Cell {:p "2" :width "40px" :class "relative"}
              [:div {:style {:paddingLeft row-indent}}
               (when (and expandable?
                          (and (= (:type row) :group)
                               (let [original-group (when (and tree-data? original-data)
                                                      (first (filter #(= (:id %) (:id row)) original-data)))]
                                 (seq (:children original-group)))))
                 [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors"
                           :on-click (fn [e]
                                      ;; Prevent event propagation to avoid interference with selection
                                       (.stopPropagation e)
                                       (js/console.log (str "Clicked expand button for: " row-id " current expanded state: " expanded?))
                                       (on-toggle-expand row-id))}
                  (if expanded?
                    [:> ChevronDown {:size 16}]
                    [:> ChevronRight {:size 16}])])]])

          ;; Data cells
           (for [{:keys [id accessor render align hidden? class]} columns
                 :when (not hidden?)
                 :let [value (if accessor
                               (accessor row)
                               (get row id))
                       display-value (if render
                                       (render value row)
                                       value)
                       is-first-col (= id (:id (first (remove :hidden? columns))))]]
             ^{:key id}
             [:> Table.Cell {:p "2"
                             :style {:textAlign align}
                             :class (str "text-[--gray-12] " class
                                         (when (and is-first-col is-child? tree-data?)
                                           " pl-0"))}

             ;; Special handling for first column in tree view
              (if (and is-first-col tree-data? is-child?)
                [:div {:style {:paddingLeft row-indent}}
                 display-value]
                display-value)])

          ;; Error indicator column
           (if (or (some row-expandable? data) tree-data?)
             (if has-error?
               [:> Table.Cell {:p "2" :width "40px" :class "relative"}
                [:div {:class "flex justify-center items-center space-x-1"}
                 [error-indicator]
                ;; Adicionar chevron para erros
                 [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors"
                           :on-click (fn [e]
                                      ;; Prevent event propagation to avoid interference with selection
                                       (.stopPropagation e)
                                       (js/console.log (str "Clicked error expand button for: " row-id " current expanded state: " expanded?))
                                       (on-toggle-expand row-id))}
                  (if expanded?
                    [:> ChevronDown {:size 16}]
                    [:> ChevronRight {:size 16}])]]]

               [:> Table.Cell {:p "2" :width "40px"}])

             [:> Table.Cell {:p "2" :width "40px"}])]

         ;; Expanded row content
          (when (and expandable? expanded?)
            [:tr
             [:td {:colSpan (+ (count (remove :hidden? columns))
                               (if on-select-row 1 0)
                               (if (or (some row-expandable? data) tree-data?) 2 0))
                   :class (str "p-0 " (when has-error? "bg-gray-900"))}

             ;; Internal rendering of expanded content
              (cond
               ;; Render error display if there's error data
                error-data
                [:div {:class "p-4 text-white"}
                 [:pre {:class "whitespace-pre-wrap"}
                  (js/JSON.stringify (clj->js error-data) nil 2)]]

               ;; Render nested resources if this is a group with children
                (and (= (:type row) :group) tree-data? original-data)
                (let [original-group (first (filter #(= (:id %) (:id row)) original-data))
                      children (:children original-group)]
                  (when (seq children)
                    [:> (.-Root Table) {:variant "ghost"
                                        :className "remove-radius-from-table"}
                     [:> Table.Body
                      (for [child children
                            :let [child-id (key-fn child)
                                  child-selected? (contains? selected-ids child-id)
                                  child-error (row-error child)
                                  has-child-error? (boolean child-error)
                                  child-expanded? (and has-child-error? (row-expanded? child))]]
                        ^{:key child-id}
                        [:<>
                         [:> Table.Row {:align "center"
                                        :class (str "border-t last:border-b "
                                                    (when child-selected? "bg-blue-50 ")
                                                    (when has-child-error? "bg-red-50 ")
                                                    "hover:bg-[--gray-3] transition-colors")}

                         ;; Selection checkbox
                          (when on-select-row
                            [:> Table.Cell {:p "2" :align "center" :width "40px"}
                             (when (selectable? child)
                               [:> Checkbox {:checked child-selected?
                                             :onCheckedChange #(on-select-row child-id (not child-selected?))
                                             :size "1"
                                             :variant "surface"
                                             :color "indigo"}])])

                         ;; Placeholder for tree expansion column (empty)
                          [:> Table.Cell {:p "2" :width "40px"}
                           [:> Box {:width "16px" :height "16px" :background "transparent"}]]

                         ;; Map through all visible columns
                          (for [{:keys [id accessor width render align hidden? class]} columns
                                :when (not hidden?)
                                :let [value (if accessor
                                              (accessor child)
                                              (get child id))
                                      display-value (if render
                                                      (render value child)
                                                      value)
                                      is-first-col (= id (:id (first (remove :hidden? columns))))]]
                            ^{:key id}
                            [:> Table.Cell {:p "2"
                                            :pl (when is-first-col "6")
                                            :style {:textAlign align}
                                            :width width
                                            :class (str "text-[--gray-12] " class)}
                             display-value])

                         ;; Error indicator and expand button
                          (if has-child-error?
                            [:> Table.Cell {:p "2" :width "40px"}
                             [:div {:class "flex justify-center items-center space-x-1"}
                              [error-indicator]
                              [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors"
                                        :on-click (fn [e]
                                                    (.stopPropagation e)
                                                    (on-toggle-expand child-id))}
                               (if child-expanded?
                                 [:> ChevronDown {:size 16}]
                                 [:> ChevronRight {:size 16}])]]]
                            [:> Table.Cell {:p "2" :width "40px"}
                             [:> Box {:width "36px" :height "36px" :background "transparent"}]])]

                        ;; Child's expanded error content if needed
                         (when (and has-child-error? child-expanded?)
                           [:tr
                            [:td {:colSpan (+ (count (remove :hidden? columns))
                                              (if on-select-row 1 0)
                                              (if (or (some row-expandable? data) tree-data?) 2 0))
                                  :class "p-0 bg-gray-900"}
                             [:div {:class "p-4 text-white"}
                              [:pre {:class "whitespace-pre-wrap"}
                               (js/JSON.stringify (clj->js child-error) nil 2)]]]])])]]))

               ;; Default case (fallback)
                :else nil)]])]))]]))

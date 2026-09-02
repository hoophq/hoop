(ns webapp.parallel-mode.components.modal.connection-list
  (:require
   ["cmdk" :refer [CommandGroup CommandItem]]
   ["@radix-ui/themes" :refer [Badge Checkbox Flex Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]
   [webapp.parallel-mode.helpers :as helpers]))

(defn connection-item
  "Single connection row. `host-label` is set only on the resource role the host
   page already has open, and names that page."
  [connection selected? host-label]
  ;; No :keywords. The gateway does the matching now, and cmdk's scorer used to
  ;; read them: the literal "connection" on every row made any subsequence of
  ;; that word match the whole list. EVL-243.
  [:> CommandItem
   {:value (:name connection)
    :onSelect #(rf/dispatch [:parallel-mode/toggle-connection connection])
    ;; cmdk owns aria-selected on the row and uses it for the keyboard
    ;; highlight, so the checked state has to be spelled out here instead.
    :aria-label (str (:name connection)
                     (when host-label (str ", " (cs/lower-case host-label)))
                     (if selected? ", checked" ", not checked"))
    :class (str "mb-2 last:mb-0 " (when selected? "bg-gray-2"))}
   [:> Flex {:align "center" :gap "3" :class "w-full"}
    [:img {:src (connection-constants/get-connection-icon connection)
           :class "w-4"
           :alt (str (:type connection) " connection icon")
           :loading "lazy"}]

    [:> Flex {:direction "column" :class "flex-1"}
     [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
      (:name connection)]]

    (when host-label
      [:> Badge {:color "indigo" :variant "soft" :size "1" :aria-hidden "true"}
       host-label])

    ;; Decorative: the row is the control. Left in the tab order the checkbox
    ;; would flip its own state on Space without dispatching anything.
    [:> Checkbox
     {:checked selected?
      :class "cursor-pointer"
      :size "2"
      :tabIndex -1
      :aria-hidden "true"}]]])

(defn- empty-state
  "`all-selected?` means the page did come back with roles and every one of them
   is already sitting in the Selected group above."
  [active-search all-selected?]
  [:div {:class "py-6 text-center text-sm text-gray-11" :role "status"}
   (cond
     all-selected? "Every resource role here is already selected"
     (cs/blank? active-search) "No resource roles found"
     :else (str "No resource role matches \"" active-search "\""))])

(defn main []
  (let [valid-connections (rf/subscribe [:parallel-mode/valid-connections])
        selected-connections (rf/subscribe [:parallel-mode/selected-connections])
        host-connection (rf/subscribe [:parallel-mode/host-connection])
        source (rf/subscribe [:parallel-mode/source])
        connections-pagination (rf/subscribe [:connections->pagination])]
    (fn []
      ;; :loading is a boolean, not a keyword. The old (= :loading ...) never
      ;; matched, so nothing guarded the next-page request.
      (let [connections-loading? (boolean (:loading @connections-pagination))
            active-search (:active-search @connections-pagination)
            searching? (not (cs/blank? active-search))
            selected @selected-connections
            selected-names (set (map :name selected))
            host-name (:name @host-connection)
            host-label (helpers/source->badge-label @source)
            label-for (fn [connection]
                        (when (= (:name connection) host-name) host-label))
            ;; Selected roles are rendered from app-db, not from the fetched
            ;; page, so they stay reachable while a search hides them and while
            ;; the user pages past them.
            rows (filterv #(not (contains? selected-names (:name %)))
                          @valid-connections)]
        [:<>
         (when (seq selected)
           [:> CommandGroup {:heading (str "Selected (" (count selected) ")")
                             :class "space-y-2 max-h-52 overflow-y-auto"}
            (doall
             (for [connection selected]
               ^{:key (str "selected-" (:name connection))}
               [connection-item connection true (label-for connection)]))])

         [:> CommandGroup (cond-> {:class "space-y-2 mb-12"}
                            (seq selected) (assoc :heading "All resource roles"))
          [infinite-scroll
           {:on-load-more (fn []
                            (when (not connections-loading?)
                              (let [current-page (:current-page @connections-pagination 1)
                                    next-page (inc current-page)
                                    next-request (cond-> {:page next-page
                                                          :force-refresh? false}
                                                   searching? (assoc :search active-search))]
                                (rf/dispatch [:connections/get-connections-paginated next-request]))))
            :has-more? (:has-more? @connections-pagination)
            :loading? connections-loading?}

           ;; has-more? guards the flash: with an empty page and more to fetch,
           ;; infinite-scroll is already loading the next one.
           (if (and (empty? rows)
                    (not connections-loading?)
                    (not (:has-more? @connections-pagination)))
             [empty-state active-search (seq @valid-connections)]
             (doall
              (for [connection rows]
                ^{:key (:name connection)}
                [connection-item connection false (label-for connection)])))]]]))))

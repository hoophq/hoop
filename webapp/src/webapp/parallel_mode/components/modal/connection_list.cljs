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
  "Single connection item with checkbox. `pinned?` marks the resource role the
   host page already has open, which parallel mode pre-selects."
  [connection selected? pinned?]
  ;; No :keywords. The gateway does the matching now, and cmdk's scorer used to
  ;; read them: the literal "connection" on every row made any subsequence of
  ;; that word match the whole list. EVL-243.
  [:> CommandItem
   {:value (:name connection)
    :onSelect #(rf/dispatch [:parallel-mode/toggle-connection connection])
    ;; cmdk owns aria-selected on the row and uses it for the keyboard
    ;; highlight, so the checked state has to be spelled out here instead.
    :aria-label (str (:name connection)
                     (when (and pinned? selected?) ", pre-selected")
                     (if selected? ", checked" ", not checked"))
    ;; A ring, not a background: [cmdk-item]:hover paints the same --gray-2
    ;; this used to use, so the marker was invisible.
    :class (str "mb-2 last:mb-0 " (when selected? "ring-1 ring-primary-8"))}
   [:> Flex {:align "center" :gap "3" :class "w-full"}
    [:img {:src (connection-constants/get-connection-icon connection)
           :class "w-4"
           :alt (str (:type connection) " connection icon")
           :loading "lazy"}]

    [:> Flex {:direction "column" :class "flex-1"}
     [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
      (:name connection)]]

    ;; Only while it is still checked. The group heading keeps saying where the
    ;; role came from after the user unchecks it.
    (when (and pinned? selected?)
      [:> Badge {:color "indigo" :variant "soft" :size "1" :aria-hidden "true"}
       "Pre-selected"])

    ;; Decorative: the row is the control. Left in the tab order the checkbox
    ;; would flip its own state on Space without dispatching anything.
    [:> Checkbox
     {:checked selected?
      :class "cursor-pointer"
      :size "2"
      :tabIndex -1
      :aria-hidden "true"}]]])

(defn main []
  (let [valid-connections (rf/subscribe [:parallel-mode/valid-connections])
        pinned-connection (rf/subscribe [:parallel-mode/pinned-connection])
        source (rf/subscribe [:parallel-mode/source])
        selected-connections (rf/subscribe [:parallel-mode/selected-connections])
        connections-pagination (rf/subscribe [:connections->pagination])]
    (fn []
      ;; :loading is a boolean, not a keyword. The old (= :loading ...) never
      ;; matched, so nothing guarded the next-page request.
      (let [connections-loading? (boolean (:loading @connections-pagination))
            active-search (:active-search @connections-pagination)
            searching? (not (cs/blank? active-search))
            selected? (fn [connection]
                        (boolean (some #(= (:name %) (:name connection))
                                       @selected-connections)))
            ;; While a search is running the pinned row is dropped rather than
            ;; matched here: repeating the gateway's predicate in CLJS would be
            ;; a second source of truth. The role still shows up in the results
            ;; with its badge if the gateway returns it.
            pinned-name (:name @pinned-connection)
            pinned (when-not searching? @pinned-connection)
            rows (if pinned
                   (filterv #(not= (:name %) (:name pinned)) @valid-connections)
                   @valid-connections)]
        [:<>
         (when pinned
           [:> CommandGroup {:heading (helpers/source->label @source)}
            [connection-item pinned (selected? pinned) true]])

         [:> CommandGroup (cond-> {:class "space-y-2 mb-12"}
                            pinned (assoc :heading "All resource roles"))
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

           (doall
            (for [connection rows]
              ^{:key (:name connection)}
              [connection-item
               connection
               (selected? connection)
               (= (:name connection) pinned-name)]))]]]))))

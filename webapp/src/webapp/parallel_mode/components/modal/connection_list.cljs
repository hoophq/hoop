(ns webapp.parallel-mode.components.modal.connection-list
  (:require
   ["cmdk" :refer [CommandGroup CommandItem]]
   ["@radix-ui/themes" :refer [Checkbox Flex Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

(defn connection-item
  "Single connection item with checkbox"
  [connection selected?]
  ;; No :keywords. The gateway does the matching now, and cmdk's scorer used to
  ;; read them: the literal "connection" on every row made any subsequence of
  ;; that word match the whole list. EVL-243.
  [:> CommandItem
   {:value (:name connection)
    :onSelect #(rf/dispatch [:parallel-mode/toggle-connection connection])
    :class (str "mb-2 last:mb-0 " (when selected? "bg-gray-2"))}
   [:> Flex {:align "center" :gap "3" :class "w-full"}
    [:img {:src (connection-constants/get-connection-icon connection)
           :class "w-4"
           :loading "lazy"}]

    [:> Flex {:direction "column" :class "flex-1"}
     [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
      (:name connection)]]

    [:> Checkbox
     {:checked selected?
      :class "cursor-pointer"
      :size "2"}]]])

(defn main []
  (let [valid-connections (rf/subscribe [:parallel-mode/valid-connections])
        selected-connections (rf/subscribe [:parallel-mode/selected-connections])
        connections-pagination (rf/subscribe [:connections->pagination])]
    (fn []
      ;; :loading is a boolean, not a keyword. The old (= :loading ...) never
      ;; matched, so nothing guarded the next-page request.
      (let [connections-loading? (boolean (:loading @connections-pagination))]
        [:> CommandGroup {:class "space-y-2 mb-12"}
         [infinite-scroll
          {:on-load-more (fn []
                           (when (not connections-loading?)
                             (let [current-page (:current-page @connections-pagination 1)
                                   next-page (inc current-page)
                                   active-search (:active-search @connections-pagination)
                                   next-request (cond-> {:page next-page
                                                         :force-refresh? false}
                                                  (not (cs/blank? active-search))
                                                  (assoc :search active-search))]
                               (rf/dispatch [:connections/get-connections-paginated next-request]))))
           :has-more? (:has-more? @connections-pagination)
           :loading? connections-loading?}

          (doall
           (for [connection @valid-connections]
             ^{:key (:name connection)}
             [connection-item
              connection
              (some #(= (:name %) (:name connection)) @selected-connections)]))]]))))

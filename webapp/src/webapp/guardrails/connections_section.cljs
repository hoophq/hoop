(ns webapp.guardrails.connections-section
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   ["@radix-ui/themes" :refer [Box Heading Grid Flex]]
   [webapp.components.multiselect :as multiselect]))

(defn format-connections-for-select [connections]
  (mapv (fn [connection]
          {:value (:id connection)
           :label (:name connection)})
        connections))

(defn main
  "Render the connections selection component for guardrails

   Parameters:
   - connections-ids: atom containing array of connection IDs
   - on-connections-change: function to call when connections are changed"
  [{:keys [connection-ids on-connections-change]}]
  (let [connections (rf/subscribe [:connections->pagination])
        search-term (r/atom "")
        search-debounce-timer (r/atom nil)]

    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])

    (fn []
      (let [connections-data (or (:data @connections) [])
            connections-loading? (:loading @connections)
            has-more? (:has-more? @connections)
            current-page (:current-page @connections 1)
            connections-options (format-connections-for-select connections-data)
            selected-values (keep (fn [id]
                                    (first (filter #(= (:value %) id) connections-options)))
                                  @connection-ids)]

        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Heading {:as "h3" :size "4" :weight "medium"} "Associate Connections"]
          [:p {:class "text-sm text-gray-500"}
           "Select the connections where this guardrail should be applied."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          [:> Flex {:direction "column" :gap "2"}
           [multiselect/paginated
            {:label "Connections"
             :options connections-options
             :default-value (if (empty? selected-values) nil (vec selected-values))
             :loading? connections-loading?
             :has-more? has-more?
             :search-value @search-term
             :placeholder "Select connections..."
             :on-change (fn [selected-options]
                          (let [new-connection-ids (mapv #(:value %) (js->clj selected-options :keywordize-keys true))]
                            (on-connections-change connection-ids new-connection-ids)))
             :on-input-change (fn [input-value]
                                (reset! search-term input-value)
                                (when @search-debounce-timer
                                  (js/clearTimeout @search-debounce-timer))
                                (let [trimmed (cs/trim input-value)
                                      should-search? (or (cs/blank? trimmed) (> (count trimmed) 2))]
                                  (when should-search?
                                    (reset! search-debounce-timer
                                            (js/setTimeout
                                             (fn []
                                               (let [request (cond-> {:page 1 :force-refresh? true}
                                                               (not (cs/blank? trimmed)) (assoc :search trimmed))]
                                                 (rf/dispatch [:connections/get-connections-paginated request])))
                                             300)))))
             :on-load-more (fn []
                             (when (not connections-loading?)
                               (let [next-page (inc current-page)
                                     active-search (:active-search @connections)
                                     next-request (cond-> {:page next-page
                                                           :force-refresh? false}
                                                    (not (cs/blank? active-search)) (assoc :search active-search))]
                                 (rf/dispatch [:connections/get-connections-paginated next-request]))))}]]]]))))
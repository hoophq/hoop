(ns webapp.components.connections-select
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.multiselect :as multiselect]))

(defn- format-connections-for-select [connections]
  (mapv (fn [connection]
          {:value (:id connection)
           :label (:name connection)})
        connections))

(defn main
  "Reusable connections selection component with pagination and search.

   Parameters:
   - connection-ids: vector containing array of connection IDs
   - selected-connections: optional vector of pre-selected connection objects with :id and :name
   - on-connections-change: function to call when connections are changed"
  [{:keys [connection-ids selected-connections on-connections-change]}]
  (let [connections (rf/subscribe [:connections->pagination])
        search-term (r/atom "")
        search-debounce-timer (r/atom nil)
        ;; Store selected connections to preserve them during searches
        selected-connections-cache (r/atom {})
        ;; Track previous connection-ids to detect changes
        prev-connection-ids (r/atom nil)]

    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])

    ;; Initialize cache with pre-selected connections if provided
    (when (seq selected-connections)
      (doseq [conn selected-connections]
        (swap! selected-connections-cache assoc (:id conn) conn)))

    (fn [{:keys [connection-ids selected-connections on-connections-change]}]
      (let [connections-data (or (:data @connections) [])
            connections-loading? (:loading @connections)
            has-more? (:has-more? @connections)
            current-page (:current-page @connections 1)

            ;; Update cache with any new connections we've loaded
            _ (doseq [conn connections-data]
                (swap! selected-connections-cache assoc (:id conn) conn))

            ;; Check if connection-ids changed and fetch missing selected connections if needed
            _ (when (not= @prev-connection-ids connection-ids)
                (reset! prev-connection-ids connection-ids)
                ;; If we have new selected IDs that aren't in our cache, we need to fetch them
                (let [missing-ids (filter #(not (contains? @selected-connections-cache %)) connection-ids)]
                  (when (seq missing-ids)
                    ;; Dispatch to get the missing connections
                    (rf/dispatch [:connections/get-connections-by-ids missing-ids]))))

            ;; Get selected connections from cache
            selected-connections-from-cache (keep #(get @selected-connections-cache %) connection-ids)
            selected-options (format-connections-for-select selected-connections-from-cache)

            ;; Format current search results
            search-options (format-connections-for-select connections-data)

            ;; Merge search results with selected options, avoiding duplicates
            search-ids (set (map :value search-options))
            missing-selected (filter #(not (search-ids (:value %))) selected-options)
            connections-options (concat search-options missing-selected)

            selected-values (keep (fn [id]
                                    (first (filter #(= (:value %) id) connections-options)))
                                  connection-ids)]
        [multiselect/paginated
         {:label "Connections"
          :options connections-options
          :default-value (if (empty? selected-values) nil (vec selected-values))
          :loading? connections-loading?
          :has-more? has-more?
          :search-value @search-term
          :placeholder "Select connections..."
          :on-change (fn [selected-options]
                       (let [selected-js-options (js->clj selected-options :keywordize-keys true)]
                         (on-connections-change selected-js-options)))
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
                              (rf/dispatch [:connections/get-connections-paginated next-request]))))}]))))
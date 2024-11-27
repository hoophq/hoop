(ns webapp.connections.views.connections-filter
  (:require [reagent.core :as r]
            [webapp.http.api :as api]
            [webapp.components.searchbox :as searchbox]))

(defn main [searched-connections-atom]
  (let [all-connections (r/atom nil)
        connections-search-status (r/atom nil)
        set-connections (fn [results]
                          (reset! all-connections results)
                          (reset! connections-search-status nil))
        set-connections-error (fn [e]
                                (println "couldn't get all connections" e)
                                (reset! connections-search-status nil))
        get-all-connections #(api/request {:method "GET"
                                           :uri "/connections"
                                           :on-success set-connections
                                           :on-failure set-connections-error})
        search (.. js/window -location -search)
        url-search-params (new js/URLSearchParams search)
        url-params-list (js->clj (for [q url-search-params] q))
        url-params-map (into (sorted-map) url-params-list)
        selected-type (r/atom (or (get url-params-map "type") ""))]
    (fn []
      [:section
       {:class "grid grid-cols-5 gap-regular"}
       [:div {:class "col-span-5"}
        [searchbox/main {:options @all-connections
                         :meta-display-keys [:type]
                         :display-key :name
                         :searchable-keys [:name :review_type :redact :type :tags :subtype]
                         :on-change-results-cb #(reset! searched-connections-atom %)
                         :hide-results-list true
                         :placeholder "Search and go to your connection"
                         :name "connection-search"
                         :clear? true
                         :loading? (= @connections-search-status :loading)
                         :on-focus (fn []
                                     (reset! connections-search-status :loading)
                                     (get-all-connections))
                         :list-classes "min-w-96"
                         :selected @selected-type}]]])))

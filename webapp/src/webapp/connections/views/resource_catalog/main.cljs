(ns webapp.connections.views.resource-catalog.main
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.resource-catalog.category-section :as category-section]
   [webapp.connections.views.resource-catalog.connection-detail-modal :as connection-detail-modal]
   [webapp.connections.views.resource-catalog.helpers :as catalog-data]
   [webapp.connections.views.resource-catalog.filters :as filters]))

(defn main []
  (let [connections-metadata (rf/subscribe [:connections->metadata])
        user (rf/subscribe [:users->current-user])
        search-term (r/atom "")
        selected-categories (r/atom #{})
        selected-tags (r/atom #{})
        selected-connection (r/atom nil)
        modal-open? (r/atom false)]

    (when (nil? @connections-metadata)
      (rf/dispatch [:connections->load-metadata]))

    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (fn []
      (if-not @connections-metadata
        [:> Box {:class "flex items-center justify-center h-screen bg-gray-50"}
         [:> Text {:size "4"} "Loading resource catalog..."]]

        (let [is-onboarding? (catalog-data/is-onboarding-context?)

              all-connections (catalog-data/compose-connections
                               (:connections @connections-metadata)
                               is-onboarding?)

              {:keys [categories tags]} (catalog-data/extract-metadata all-connections)

              filter-params {:search-term @search-term
                             :selected-categories @selected-categories
                             :selected-tags @selected-tags}
              filtered-connections (catalog-data/apply-filters all-connections filter-params)

              has-any-filter? (catalog-data/has-active-filters? filter-params)
              popular-connections (catalog-data/get-popular-connections
                                   all-connections is-onboarding? has-any-filter?)

              connections-by-category (->> filtered-connections
                                           (group-by :category)
                                           (into (sorted-map)))]

          [:> Box {:class "h-screen bg-gray-50 flex overflow-hidden"}
           [:> Box {:class "w-80 flex flex-col"}
            [:> Box {:class "p-6 space-y-radix-8 flex-1 overflow-y-auto"}
             [filters/search-section @search-term #(reset! search-term %)]
             [filters/categories-filter @selected-categories
              (fn [category]
                (if (contains? @selected-categories category)
                  (swap! selected-categories disj category)
                  (swap! selected-categories conj category)))
              categories]
             [filters/tags-filter @selected-tags
              (fn [tag]
                (if (contains? @selected-tags tag)
                  (swap! selected-tags disj tag)
                  (swap! selected-tags conj tag)))
              tags]]]

           [:> Box {:class "flex-1 flex flex-col overflow-hidden"}
            [:> Box {:class "p-8 flex-1 overflow-y-auto"}
             [:> Box {:class "max-w-7xl space-y-radix-9 mx-auto"}
              [:> Box {:class "space-y-radix-6 mb-12"}
               (when is-onboarding?
                 [:figure
                  [:img {:src "/images/hoop-branding/PNG/hoop-symbol_black@4x.png"
                         :alt "Hoop Logo"
                         :class "w-16"}]])
               [:> Box
                [:> Heading {:as "h2" :size "6" :weight "bold" :class "mb-3 text-[--gray-12]"}
                 "Getting Started"]
                [:> Text {:as "p" :size "3" :class "text-[--gray-12]"}
                 "Setup your environment by selecting your Resource type:"]]]

              (when (seq popular-connections)
                [category-section/main "Popular" popular-connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])

              (for [[category connections] connections-by-category]
                ^{:key category}
                [category-section/main (cs/replace (cs/capitalize category) #"-" " ")
                 connections
                 (fn [connection]
                   (reset! selected-connection connection)
                   (reset! modal-open? true))])]]]

           [connection-detail-modal/main @selected-connection @modal-open?
            #(reset! modal-open? false)
            (-> @user :data :admin?)]])))))

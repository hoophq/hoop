(ns webapp.audit.views.audit-filters
  (:require [reagent.core :as r]
            [re-frame.core :as rf]
            [clojure.string :as string]
            [webapp.components.forms :as forms]
            [webapp.components.combobox :as combobox]))

(defn- form [_ _ _ connection-id]
  (let [user (rf/subscribe [:users->current-user])
        users (rf/subscribe [:users])
        connections-combobox-status (r/atom nil)
        users-options (fn [users]
                        (reset! connections-combobox-status :ready)
                        (map #(into {} {:value (:email %)
                                        :text (:email %)}) users))
        dispatch-date (fn [{:keys [date filter-key]}]
                        (let [iso-date (when (not (string/blank? date))
                                         (.toISOString
                                          (new js/Date
                                               (if (= filter-key "start_date")
                                                 (str date " 00:00:00.000Z")
                                                 (str date " 23:59:59.000Z")))))]
                          (rf/dispatch [:audit->filter-sessions {filter-key iso-date} connection-id])))]

    (fn [filters search-value filter-type connection-id]
      (let [start-date (if-let [date (get filters "start_date")]
                         (subs date 0 10) "")
            end-date (if-let [date (get filters "end_date")]
                       (subs date 0 10) "")]
        [:div {:class "grid grid-cols-2 gap-y-regular lg:grid-cols-5 lg:gap-regular"}
         (when (-> @user :data :admin?)
           [:div {:class "col-span-2"}
            [combobox/main {:options (users-options @users)
                            :selected (or (:email (first
                                                   (filter
                                                    #(= (:id %) (get filters "user"))
                                                    @users))) "")
                            :label "User"
                            :clear? true
                            :loading? (= @connections-combobox-status :loading)
                            :on-focus (fn []
                                        (rf/dispatch [:users->get-users])
                                        (reset! connections-combobox-status :loading))
                            :default-value "Select a user"
                            :placeholder "Select one"
                            :list-classes "min-w-64"
                            :on-change (fn [user-email]
                                         (let [u (first (filter
                                                         #(= user-email (:email %))
                                                         @users))
                                               user-id (:id u)]
                                           (reset! search-value (str @search-value " user:" user-email))
                                           (reset! filter-type "basic")
                                           (rf/dispatch [:indexer-plugin->clear-search-results])
                                           (rf/dispatch [:audit->filter-sessions {"user" user-id} connection-id])))
                            :name "select-user"}]])
         [:div {:class "grid grid-cols-2 gap-regular col-span-3"}
          [:div
           [forms/input {:label "From"
                         :type "date"
                         :id :end-date
                         :name :end-date
                         :value start-date
                         :on-change (fn [element]
                                      (rf/dispatch [:indexer-plugin->clear-search-results])
                                      (reset! filter-type "basic")
                                      (dispatch-date {:date (-> element .-target .-value)
                                                      :filter-key "start_date"}))}]]
          [:div
           [forms/input {:label "To"
                         :type "date"
                         :id :end-date
                         :name :end-date
                         :min start-date
                         :value end-date
                         :on-change (fn [element]
                                      (rf/dispatch [:indexer-plugin->clear-search-results])
                                      (reset! filter-type "basic")
                                      (dispatch-date {:date (-> element .-target .-value)
                                                      :filter-key "end_date"}))}]]]]))))

(defn audit-filters [_]
  (fn [filters search-value filter-type connection-id]
    [form filters search-value filter-type connection-id]))

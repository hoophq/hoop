(ns webapp.audit.views.session-data-raw
  (:require [reagent.core :as r]
            [webapp.audit.views.empty-event-stream :as empty-event-stream]
            [webapp.components.icon :as icon]
            [webapp.components.searchbox :as searchbox]
            [webapp.formatters :as formatters]
            [webapp.utilities :as utilities]))

(defn- event-item []
  (let [is-open? (r/atom false)]
    (fn [event-type data parsed-date]
      [:div
       {:class (str "flex flex-col gap-small transition"
                    (when (= event-type "i") " bg-gray-50 hover:bg-gray-100")
                    (when (= event-type "o") " bg-white hover:bg-gray-50")
                    (when (= event-type "e") " bg-red-100 hover:bg-red-200")
                    " last:border-0 border-b px-regular")}
       [:div
        {:class "flex items-center gap-small cursor-pointer py-regular"
         :on-click #(reset! is-open? (not @is-open?))}
        [:span {:class "font-mono truncate text-xs flex-1"}
         (str (when (= event-type "i") "> ") data)]
        [:span
         {:class "flex-grow text-right text-xs"}
         parsed-date]
        [:span [icon/regular
                {:size 5
                 :icon-name (if @is-open?
                              "cheveron-up"
                              "cheveron-down")}]]]
       (when @is-open?
         [:div
          {:class (str "bg-gray-700 font-mono overflow-auto"
                       " whitespace-pre text-white text-sm"
                       " p-regular rounded-lg mb-regular")}
          data])])))

(defn event-stream-content [event-stream session-start-date]
  (let [event-stream-map (for [[seconds event-type event-data] event-stream]
                           {:seconds seconds
                            :parsed-date ((fn []
                                            (let [milliseconds (int (* seconds 1000))
                                                  date (new js/Date session-start-date)
                                                  get-time (.getTime date)
                                                  sum (+ milliseconds get-time)
                                                  parsed-new-date (new js/Date sum)]
                                              (formatters/time-parsed->full-date parsed-new-date))))
                            :event-type event-type
                            :event-data (utilities/decode-b64 event-data)})
        searched-events-atom (r/atom event-stream-map)
        search-focused? (r/atom false)]
    (fn []
      [:section {:class "grid gap-small"}
       [:header {:id "session-details-search-container"}
        [:div {:class (str "transition-all " (if @search-focused? "w-7/12" "w-1/2"))}
         [searchbox/main {:options event-stream-map
                          :clear? true
                          :hide-results-list true
                          :on-change-results-cb #(reset! searched-events-atom %)
                          :on-focus #(reset! search-focused? true)
                          :on-blur #(reset! search-focused? false)
                          :display-key :event-data
                          :name "session-details-search"
                          :placeholder "Search content in the queries below"
                          :searchable-keys [:event-data :parsed-date]}]]]
       [:div
        {:class (str "rounded-lg border overflow-hidden "
                     "flex flex-col whitespace-pre")}
        (doall
         (for [{:keys [seconds event-type event-data parsed-date]} @searched-events-atom]
           ^{:key seconds}
           [event-item event-type event-data parsed-date]))]])))

(defn main [event-stream session-start-date]
  (if (empty? event-stream)
    [empty-event-stream/main]
    [event-stream-content event-stream session-start-date]))


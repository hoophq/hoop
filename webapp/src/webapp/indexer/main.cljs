(ns webapp.indexer.main
  (:require
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.formatters :as formatters]
   [webapp.components.user-icon :as user-icon]
   [webapp.components.icon :as icon]
   [webapp.components.headings :as h]
   [webapp.components.button :as button]
   [webapp.components.forms :as forms]
   [webapp.audit.views.session-details :as session-details]
   [webapp.config :as config]))

(def search-fields
  [{:field "session" :action "session: "}
   {:field "connection" :action "connection: "}
   {:field "connection_type" :action "connection_type:command-line "}
   {:field "user" :action "user:user@email.com "}
   {:field "verb" :action "verb:connect "}
   {:field "size" :action "size:>10000 "}
   {:field "input" :action "in:input "}
   {:field "output" :action "in:output "}
   {:field "error" :action "is:error "}
   {:field "truncated" :action "is:truncated "}
   {:field "duration" :action "duration:>30 "}
   {:field "started" :action "started:>YYYY-MM-DD "}
   {:field "completed" :action "completed:>YYYY-MM-DD "}])

(defn result-item [{:keys [fragments
                           session-id
                           date-completed
                           user-name
                           connection-name
                           connection-type]}]
  [:div
   {:class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-50"
                " p-regular text-sm grid grid-cols-6 gap-large")
    :on-click (fn []
                (rf/dispatch [:open-modal
                              [session-details/main {:id session-id}] :large]))}
   [:div#connection-info
    {:class "flex flex-col gap-small justify-center"}
    [:div
     [:b connection-name]]
    [:div
     {:class "text-xxs text-gray-800"}
     [:span connection-type]]]
   [:div#user-info
    {:class "flex items-center"}
    [:div {:class "flex gap-small items-center"}
     [user-icon/initials-black user-name]
     [:div
      {:class "text-gray-800 text-xs"}
      user-name]]]
   [:div#session-id
    {:class "flex gap-regular text-xs items-center"}
    [:span {:class "text-gray-400"}
     "id:"]
    [:span {:class "text-gray-800"}
     (take 8 session-id)]]
   [:div#fragments
    {:class "col-span-2 justify-center"}
    [:div {:class (str "bg-gray-100 rounded-lg overflow-hidden whitespace-pre-wrap break-words"
                       " w-full h-full p-small text-gray-800 text-xs")}
     [:div {:dangerouslySetInnerHTML {:__html fragments}}]]]
   [:div {:id "time-info"}
    [:div {:class (str "flex items-center gap-small justify-center text-xs"
                       " p-regular rounded-lg bg-gray-100 text-gray-800")}
     [icon/regular {:icon-name "watch-black"
                    :size 4}]
     [:span (formatters/time-ago-full-date date-completed)]]]])

(defn search-results-list []
  (let [search-results (rf/subscribe [:indexer-plugin->search])
        users (rf/subscribe [:users])
        fragments-to-string (fn [fragments]
                              (first (for [[_ v] fragments]
                                       (str v " "))))]
    (rf/dispatch [:users->get-users])
    (fn []
      [:section {:id "indexer-search-results-list"
                 :class "border overflow-hidden rounded-lg"}
       (doall
        (for [item (-> @search-results :results :hits)]
          ^{:key (str
                  (:id item)
                  (.toString js/JSON (clj->js (:locations item))))}
          [result-item {:fragments (fragments-to-string
                                    (-> item :fragments))
                        :date-completed (-> item :fields :completed)
                        :connection-name (-> item :fields :connection)
                        :connection-type (-> item :fields :connection_type)
                        :session-id (:id item)
                        :user-name (:name (first
                                           (filter
                                            #(= (:email %)
                                                (-> item :fields :user))
                                            @users)))}]))])))

(defn empty-list-view []
  [:div {:class "py-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no results to look"]
    [:div {:class "text-gray-500 text-xs"}
     "There's nothing with this criteria"]
    [:div {:class "text-gray-600 text-xs"}
     "Trouble writing the search query? "
     [:a {:href "https://hoop.dev/docs/plugins/indexer/search-syntax/"
          :target "_blank"
          :class "underline text-blue-500"}
      "Get to know how to use our search syntax"]]]])

(defn panel []
  (let [search-value (r/atom "in:input ")
        search-results (rf/subscribe [:indexer-plugin->search])]
    (fn []
      [:div
       {:class "px-regular"}
       [:header {:class "mb-regular"}
        [h/h1 "Search on sessions"]]
       [:section
        [:div
         {:class "flex gap-regular"}
         [:form {:class "flex-grow"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (rf/dispatch [:indexer-plugin->search {:query @search-value
                                                                     :fields search-fields}]))}
          [forms/input {:defaultValue @search-value
                        :on-change #(reset! search-value (-> % .-target .-value))}]]
         [button/black {:text "Search"
                        :on-click #(rf/dispatch [:indexer-plugin->search {:query @search-value
                                                                          :fields search-fields}])
                        :type "submit"}]]]
       [:div {:class "grid grid-cols-8"}
        [:div
         [:header
          {:class "mb-small"}
          [h/h4 "Available fields"]]
         [:section
          {:class "flex flex-col gap-0.5"}
          (for [item search-fields]
            ^{:key (:field item)}
            [:div
             {:class "text-sm hover:text-blue-500 cursor-pointer"
              :on-click #(reset! search-value (str @search-value " " (:action item)))}
             (:field item)])]]
        [:div {:class "col-span-7"}
         (if (seq (-> @search-results :results :hits))
           [search-results-list]
           [empty-list-view])]]])))

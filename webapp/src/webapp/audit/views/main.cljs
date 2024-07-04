(ns webapp.audit.views.main
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.audit.views.audit-filters :as filters]
            [webapp.audit.views.sessions-list :as sessions-list]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.loaders :as loaders]
            [webapp.indexer.main :as indexer]
            [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]))

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

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defmulti filter-type-response identity)
(defmethod filter-type-response "basic" [_ sessions _]
  [:div {:class "rounded-lg border overflow-hidden bg-white"}
   [sessions-list/sessions-list
    (:sessions @sessions)
    (:status @sessions)]])

(defmethod filter-type-response "indexer" [_ _ search-results]
  (cond
    (= :loading (:status @search-results)) [loading-list-view]
    (not (seq (-> @search-results :results :hits))) [indexer/empty-list-view]
    :else [indexer/search-results-list]))

(defn- indexer-modal []
  [:div {:class "h-screen-90vh"}
   [:header
    [h/h2 "Indexer Plugin"]
    [:article {:class "text-sm text-gray-500 mt-small mb-regular"}
     "Enable your connections below so you can search for whatever you want in your sessions."]
    [plugin-configuration-container/main]]])

(defn panel [_]
  (let [sessions (rf/subscribe [:audit])
        search-value (r/atom "in:input ")
        search-results (rf/subscribe [:indexer-plugin->search])
        filter-type (r/atom "basic")]
    (rf/dispatch [:plugins->get-plugin-by-name "indexer"])
    (fn [connection]
      [:div {:class "h-full px-large flex flex-col bg-white rounded-lg py-regular"}
       [:header
        [filters/audit-filters
         (:filters @sessions)
         search-value
         filter-type
         (:id connection)]
        [:section
         [:form {:class "flex items-center gap-regular"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (reset! filter-type "indexer")
                              (rf/dispatch [:indexer-plugin->clear-search-results])
                              (rf/dispatch [:indexer-plugin->search {:query @search-value
                                                                     :fields search-fields}]))}
          [:div {:class "relative w-full"}
           [forms/input {:label "Indexer"
                         :value @search-value
                         :on-change #(reset! search-value (-> % .-target .-value))}]
           [:span {:class "absolute inset-y-0 right-0 pr-3 pt-2 flex items-center pointer-events-none"}
            [icon/regular {:size 4
                           :icon-name "search"}]]]
          [:div {:role "button"
                 :tab-index "0"
                 :on-click #(rf/dispatch [:open-modal [indexer-modal] :large])
                 :on-keyDown (fn [e]
                               (when (or (= (.-keyCode e) 13) (= (.-keyCode e) 32))
                                 (rf/dispatch [:open-modal [indexer-modal] :large])))}
           [icon/regular {:size 6
                          :icon-name "help-with-circle-black"}]]]]]
       [filter-type-response @filter-type sessions search-results]])))

(defn main [_]
  (fn [connection]
    (rf/dispatch [:audit->filter-sessions {"connection" (:name connection)} (:id connection)])
    [panel connection]))

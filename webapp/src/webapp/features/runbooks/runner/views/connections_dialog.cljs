(ns webapp.features.runbooks.runner.views.connections-dialog
  (:require 
   ["cmdk" :refer [CommandGroup CommandItem]] 
   ["lucide-react" :refer [ChevronRight]]
   ["@radix-ui/themes" :refer [Flex Text]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.command-dialog :as command-dialog]))

(defn- connection-result-item
  "Connection search result item"
  [connection]
  [:> CommandItem
   {:key (:id connection)
    :value (:name connection)
    :keywords [(:type connection) (:subtype connection) (:status connection) "connection"]
    :onSelect #(do
                 (rf/dispatch [:runbooks/set-selected-connection connection])
                 (rf/dispatch [:runbooks/toggle-connection-dialog false]))}
   [:> Flex {:align "center" :gap "2"}
    [:img {:src (connection-constants/get-connection-icon connection)
           :class "w-4"
           :loading "lazy"}]
    [:> Flex {:direction "column"}
     [:> Text {:size "2" :class "text-[--gray-11]"} (:name connection)]]
    [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]])

(defn- connections-list
  "Connections-only list with search results"
  [search-results]
  (let [search-status (:status search-results)
        connections (:connections (:data search-results))]
    [:<>
     (when (and (= search-status :ready) (seq connections))
       [:> CommandGroup
        (for [connection connections]
          ^{:key (:id connection)}
          [connection-result-item connection])])]))

(defn connections-dialog []
  (let [open (rf/subscribe [:runbooks/connection-dialog-open?])
        connections (rf/subscribe [:connections])
        search-term (r/atom "")]
    (fn []
      [command-dialog/command-dialog
       {:open? @open
        :on-open-change (fn [open?]
                          (rf/dispatch [:runbooks/toggle-connection-dialog open?])
                          (when-not open? (reset! search-term "")))
        :title "Select or search a connection"
        :search-config {:show-search-icon true
                        :show-input true
                        :placeholder "Select or search a connection"
                        :value @search-term
                        :on-value-change (fn [value]
                                           (reset! search-term value))
                        :on-key-down (fn [e]
                                       (when (= (.-key e) "Escape")
                                         (.preventDefault e)
                                         (rf/dispatch [:runbooks/toggle-connection-dialog false])
                                         (reset! search-term "")))}
        :breadcrumb-config {:context "Runbooks" :current-page "Connections"}
        :content
        [connections-list {:status :ready :data {:connections (:results @connections)}}]}])))

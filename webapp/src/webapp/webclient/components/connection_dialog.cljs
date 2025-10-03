(ns webapp.webclient.components.connection-dialog
  (:require
   ["cmdk" :refer [CommandGroup CommandItem]]
   ["lucide-react" :refer [ChevronRight]]
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.command-dialog :as command-dialog]))

(defn- connection-result-item
  "Connection search result item"
  [connection selected?]
  [:> CommandItem
   {:key (:id connection)
    :value (:name connection)
    :keywords [(:type connection) (:subtype connection) (:status connection) "connection"]
    :onSelect #(do
                 (rf/dispatch [:primary-connection/set-selected connection])
                 (rf/dispatch [:primary-connection/toggle-dialog false]))}
   [:> Flex {:align "center" :gap "2" :class "w-full"}
    [:img {:src (connection-constants/get-connection-icon connection)
           :class "w-4"
           :loading "lazy"}]
    [:> Flex {:direction "column" :class "flex-1"}
     [:> Text {:size "2" :class (if selected? "text-primary-11" "text-[--gray-11]")}
      (:name connection)]
     (when (= (:status connection) "offline")
       [:> Text {:size "1" :color "gray"} "Offline"])]
    (when selected?
      [:> Badge {:color "indigo" :size "1"} "Selected"])
    [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]])

(defn- connections-list
  "Connections list with search results"
  [connections selected-connection]
  [:<>
   (when (seq connections)
     [:> CommandGroup
      (for [connection connections]
        ^{:key (:id connection)}
        [connection-result-item connection (= (:name connection) (:name selected-connection))])])])

(defn connection-dialog []
  (let [open? (rf/subscribe [:primary-connection/dialog-open?])
        connections (rf/subscribe [:primary-connection/list])
        selected (rf/subscribe [:primary-connection/selected])
        search-term (r/atom "")]

    (rf/dispatch [:primary-connection/initialize-with-persistence])

    (fn []
      (let [all-connections (or @connections [])
            valid-connections (filter #(and
                                        (not (#{"tcp" "httpproxy" "ssh"} (:subtype %)))
                                        (or (= "enabled" (:access_mode_exec %))
                                            (= "enabled" (:access_mode_runbooks %))))
                                      all-connections)
            query (-> @search-term (or "") cs/trim cs/lower-case)
            matches? (fn [connection]
                       (let [name (some-> (:name connection) cs/lower-case)]
                         (and name (cs/includes? name query))))
            filtered-connections (if (cs/blank? query)
                                   valid-connections
                                   (filter matches? valid-connections))]
        [command-dialog/command-dialog
         {:open? @open?
          :on-open-change (fn [open?]
                            (rf/dispatch [:primary-connection/toggle-dialog open?])
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
                                           (rf/dispatch [:primary-connection/toggle-dialog false])
                                           (reset! search-term "")))}
          :breadcrumb-config {:context "Terminal" :current-page "Connections"}
          :content
          [connections-list filtered-connections @selected]}]))))


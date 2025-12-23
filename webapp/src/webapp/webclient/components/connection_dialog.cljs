(ns webapp.webclient.components.connection-dialog
  (:require
   ["cmdk" :refer [CommandGroup CommandItem]]
   ["lucide-react" :refer [ChevronRight]]
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.command-dialog :as command-dialog]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

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
        selected (rf/subscribe [:primary-connection/selected])
        connections (rf/subscribe [:connections->pagination])
        search-term (r/atom "")
        search-debounce-timer (r/atom nil)]

    (rf/dispatch [:primary-connection/initialize-with-persistence])

    (fn []
      (let [all-connections (or (:data @connections) [])
            connections-loading? (= :loading (:loading @connections))
            valid-connections (filter #(and
                                        (not (#{"tcp" "httpproxy" "ssh"} (:subtype %)))
                                        (= "enabled" (:access_mode_exec %)))
                                      all-connections)]
        [command-dialog/command-dialog
         {:open? @open?
          :loading? connections-loading?
          :on-open-change (fn [open?]
                            (rf/dispatch [:primary-connection/toggle-dialog open?])
                            (when-not open?
                              (when (not (cs/blank? @search-term))
                                (reset! search-term "")
                                (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])

                                (when @search-debounce-timer
                                  (js/clearTimeout @search-debounce-timer)
                                  (reset! search-debounce-timer nil)))))
          :title "Select or search a resource role"
          :search-config {:show-search-icon true
                          :show-input true
                          :placeholder "Select or search a resource role"
                          :value @search-term
                          :on-value-change (fn [value]
                                             (reset! search-term value)
                                             (when @search-debounce-timer
                                               (js/clearTimeout @search-debounce-timer))
                                             (let [trimmed (cs/trim value)
                                                   should-search? (or (cs/blank? trimmed) (> (count trimmed) 2))]
                                               (when should-search?
                                                 (reset! search-debounce-timer
                                                         (js/setTimeout
                                                          (fn []
                                                            (let [request (cond-> {:page 1 :force-refresh? true}
                                                                            (not (cs/blank? trimmed)) (assoc :search trimmed))]
                                                              (rf/dispatch [:connections/get-connections-paginated request])))
                                                          500)))))
                          :on-key-down (fn [e]
                                         (when (= (.-key e) "Escape")
                                           (.preventDefault e)
                                           (rf/dispatch [:primary-connection/toggle-dialog false])
                                           (reset! search-term "")
                                           (when @search-debounce-timer
                                             (js/clearTimeout @search-debounce-timer)
                                             (reset! search-debounce-timer nil))))}
          :breadcrumb-config {:context "Terminal" :current-page "Resource Roles"}
          :content
          [infinite-scroll
           {:on-load-more (fn []
                            (when (not connections-loading?)
                              (let [current-page (:current-page @connections 1)
                                    next-page (inc current-page)
                                    active-search (:active-search @connections)
                                    next-request (cond-> {:page next-page
                                                          :force-refresh? false}
                                                   (not (cs/blank? active-search)) (assoc :search active-search))]
                                (rf/dispatch [:connections/get-connections-paginated next-request]))))
            :has-more? (:has-more? @connections)
            :loading? connections-loading?}
           [connections-list valid-connections @selected]]}]))))

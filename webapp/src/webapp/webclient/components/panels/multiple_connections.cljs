(ns webapp.webclient.components.panels.multiple-connections
  (:require
   ["@radix-ui/themes" :refer [Box Badge Flex IconButton Text Spinner]]
   ["lucide-react" :refer [Plus Minus]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn connection-item [{:keys [connection selected? on-select disabled?]} dark-mode?]
  [:> Flex {:align "center"
            :justify "between"
            :p "2"
            :class (str "py-3 "
                        (when (= "offline" (:status connection)) "opacity-50 ")
                        (when disabled? "opacity-70 cursor-not-allowed ")
                        (when selected? "bg-primary-11 light text-gray-1"))}
   [:> Flex {:align "center" :gap "2"}
    [:> Box {:class "w-4"}
     [:img {:src (connection-constants/get-connection-icon connection "rounded")
            :class "w-4"}]]
    [:> Text {:size "2"
              :weight "medium"}
     (:name connection)]]

   (when (= "online" (:status connection))
     (if selected?
       [:> IconButton {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click on-select
                       :class (when @dark-mode? "dark")}
        [:> Minus {:size 16}]]
       [:> IconButton {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click on-select
                       :class (when @dark-mode? "dark")}
        [:> Plus {:size 16}]]))])

(defn filter-connections [connections]
  (filterv #(and (not (#{"tcp" "httpproxy"} (:subtype %)))
                 (or (= "enabled" (:access_mode_exec %))
                     (= "enabled" (:access_mode_runbooks %))))
           connections))

(defn filter-compatible-connections [connections
                                     main-connection
                                     selected-connections]
  (let [connection (if main-connection
                     main-connection
                     (first selected-connections))]
    (filterv #(and (= (:type %) (:type connection))
                   (= (:subtype %) (:subtype connection))
                   (not= (:name %) (:name connection))
                   (not= (:name %) (:name connection)))
             connections)))

(defn connections-list []
  (let [primary-connection (rf/subscribe [:primary-connection/selected])
        selected-connections (rf/subscribe [:multiple-connections/selected])]
    (fn [dark-mode? connections]
      (let [filtered-connections (filter-connections connections)
            filtered-compatible-connections (filter-compatible-connections filtered-connections
                                                                           @primary-connection
                                                                           @selected-connections)
            compatible-connections (if @primary-connection
                                     filtered-compatible-connections
                                     filtered-connections)]
        [:> Box
         (when @primary-connection
           [connection-item
            {:connection @primary-connection
             :selected? true
             :disabled? true
             :on-select (fn []
                          (rf/dispatch [:connections->get-connections])
                          (rf/dispatch [:primary-connection/toggle-dialog true]))}
            dark-mode?])
         (for [connection compatible-connections]
           ^{:key (:name connection)}
           [connection-item
            {:connection connection
             :selected? (some #(= (:name %) (:name connection)) @selected-connections)
             :on-select #(rf/dispatch [:multiple-connections/toggle connection])}
            dark-mode?])]))))

(defn main [dark-mode?]
  (let [total-count (rf/subscribe [:execution/total-count])
        connections (rf/subscribe [:connections])]
    (fn []
      [:> Box {:class "h-full flex flex-col"}
       [:> Flex {:align "center"
                 :gap "2"
                 :class "border-b border-gray-3 px-4 py-3"}
        [:> Text {:size "3" :weight "bold" :class "text-gray-12"} "Multi Run"]
        [:> Badge {:variant "solid" :color "green" :radius "full"}
         [:> Flex {:align "center" :gap "2"}
          [:> Text {:size "1" :weight "medium" :class "text-white"} "Selected"]
          [:> Badge {:variant "solid" :radius "full" :class "bg-white" :size "1"}
           [:> Text {:size "1" :weight "bold" :class "text-success-9"}
            @total-count]]]]]


       [:> Box {:class "space-y-4 text-gray-11"}
        [:> Text {:as "p" :size "1" :class "py-3 px-4"}
         "Select similar connections to execute commands at once."]

        (if (:loading @connections)
          [:> Box {:class "flex items-center justify-center"}
           [:> Spinner {:size "2"}]]
          [connections-list dark-mode? (:results @connections)])]])))

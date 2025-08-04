(ns webapp.webclient.components.panels.multiple-connections
  (:require
   ["@radix-ui/themes" :refer [Box Badge Flex IconButton Text]]
   ["lucide-react" :refer [Plus Minus]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn connection-item [{:keys [connection selected? on-select disabled?]} dark-mode?]
  [:> Flex {:align "center"
            :justify "between"
            :p "2"
            :class (str "px-2 py-3 "
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
                                     selected-connections
                                     runbooks-panel-opened?]
  (let [connection (if main-connection
                     main-connection
                     (first selected-connections))
        is-same-access-mode #(if runbooks-panel-opened?
                               (= (:access_mode_runbooks %) (:access_mode_runbooks connection))
                               (= (:access_mode_exec %) (:access_mode_exec connection)))]
    (filterv #(and (= (:type %) (:type connection))
                   (= (:subtype %) (:subtype connection))
                   (not= (:name %) (:name connection))
                   (not= (:name %) (:name connection))
                   (is-same-access-mode %))
             connections)))

(defn connections-list [dark-mode? runbooks-panel-opened?]
  (let [connections @(rf/subscribe [:primary-connection/filtered])
        primary-connection @(rf/subscribe [:primary-connection/selected])
        selected-connections @(rf/subscribe [:multiple-connections/selected])
        filtered-connections (filter-connections connections)
        filtered-compatible-connections (filter-compatible-connections filtered-connections
                                                                       primary-connection
                                                                       selected-connections
                                                                       runbooks-panel-opened?)
        compatible-connections (if primary-connection
                                 filtered-compatible-connections
                                 filtered-connections)]
    [:> Box
     (when primary-connection
       [connection-item
        {:connection primary-connection
         :selected? true
         :disabled? true} dark-mode?])

     (for [connection compatible-connections]
       ^{:key (:name connection)}
       [connection-item
        {:connection connection
         :selected? (some #(= (:name %) (:name connection)) selected-connections)
         :on-select #(rf/dispatch [:multiple-connections/toggle connection])} dark-mode?])]))

(defn main [dark-mode? runbooks-panel-opened?]
  (let [selected-connections @(rf/subscribe [:multiple-connections/selected])
        total-count @(rf/subscribe [:execution/total-count])]
    [:> Box {:class "h-full flex flex-col"}
     [:> Flex {:justify "between"
               :align "center"
               :class "px-2 py-3 border-b border-gray-3"}
      [:> Text {:size "3" :weight "bold" :class "text-gray-12"}
       [:> Flex {:gap "2" :align "center"}
        [:> Text {:size "3" :weight "bold" :class "text-gray-12"} "MultiRun"]
        [:> Badge {:variant "solid" :color "green" :radius "full"}
         [:> Flex {:align "center" :gap "2"}
          [:> Text {:size "1" :weight "medium" :class "text-white"} "Selected"]
          [:> Badge {:variant "solid" :radius "full" :class "bg-white"}
           [:> Text {:size "1" :weight "bold" :class "text-success-9"}
            total-count]]]]]]]


     [:> Box {:class "space-y-4 text-gray-11"}
      [:> Text {:as "p" :size "1" :class "px-2 py-3"}
       "Select similar connections to execute commands at once."]

      [connections-list dark-mode? runbooks-panel-opened?]]]))

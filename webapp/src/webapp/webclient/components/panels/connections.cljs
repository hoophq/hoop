(ns webapp.webclient.components.panels.connections
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
     [:img {:src (connection-constants/get-connection-icon connection (if dark-mode?
                                                                        :light
                                                                        :dark))
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
                       :class (when dark-mode? "dark")}
        [:> Minus {:size 16}]]
       [:> IconButton {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click on-select
                       :class (when dark-mode? "dark")}
        [:> Plus {:size 16}]]))])

(defn filter-compatible-connections [connections main-connection selected-connections]
  (let [connection (if main-connection
                     main-connection
                     (first selected-connections))]
    (filterv #(and (= (:type %) (:type connection))
                   (= (:subtype %) (:subtype connection))
                   (not= (:name %) (:name connection)))
             connections)))

(defn connections-list [dark-mode?]
  (let [connections @(rf/subscribe [:connections/filtered])
        primary-connection @(rf/subscribe [:connections/selected])
        selected-connections @(rf/subscribe [:connection-selection/selected])
        filtered-connections (filter-compatible-connections connections primary-connection selected-connections)
        compatible-connections (if (seq filtered-connections)
                                 filtered-connections
                                 connections)]
    [:> Box
     ;; Conexão principal fixa
     [connection-item
      {:connection primary-connection
       :selected? true
       :disabled? true} dark-mode?]

     ;; Outras conexões compatíveis
     (for [connection compatible-connections]
       ^{:key (:name connection)}
       [connection-item
        {:connection connection
         :selected? (some #(= (:name %) (:name connection)) selected-connections)
         :on-select #(rf/dispatch [:connection-selection/toggle connection])} dark-mode?])]))

(defn main [dark-mode?]
  (let [selected-connections @(rf/subscribe [:connection-selection/selected])]
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
            (+ (count selected-connections) 1)]]]]]]]


     [:> Box {:class "space-y-4 text-gray-11"}
      [:> Text {:as "p" :size "1" :class "px-2 py-3"}
       "Select similar connections to execute commands at once."]

      [connections-list dark-mode?]]]))

(ns webapp.webclient.components.connections-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton Text]]
   ["lucide-react" :refer [FolderTree Settings2 EllipsisVertical X]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.constants :as connection-constants]
   [webapp.webclient.components.database-schema :as database-schema]))

(defn connection-item [{:keys [name type subtype status selected? on-select dark? admin?]}]
  [:> Box {:class (str "flex justify-between items-center px-2 py-3 "
                       "transition "
                       (if selected?
                         "text-gray-1 bg-primary-11 hover:bg-primary-11 active:bg-primary-12"
                         "text-gray-12 hover:bg-primary-3 active:bg-primary-4")
                       (when dark? "dark"))
           :onClick (when (not= status "offline") on-select)}
   [:> Flex {:align "center" :gap "2" :justify "between" :class "w-full"}
    [:> Flex {:align "center" :gap "2"}
     [:> Box
      [:figure {:class "w-4"}
       [:img {:src (connection-constants/get-connection-icon
                    {:type type :subtype subtype}
                    (if dark?
                      :light
                      :dark))
              :class "w-4"}]]]
     [:div {:class "flex flex-col"}
      [:> Text {:size "2"} name]
      (when (= status "offline")
        [:> Text {:size "1" :color "gray"} "Offline"])]]

    (when-not (or selected?
                  (not admin?))
      [:> IconButton
       {:variant "ghost"
        :color "gray"
        :onClick (fn [e]
                   (.stopPropagation e)
                   (rf/dispatch [:navigate :edit-connection {} :connection-name name]))}
       [:> EllipsisVertical {:size 16}]])]])

(defn selected-connection []
  (let [show-schema? (r/atom false)]
    (fn [connection dark-mode? admin?]
      [:> Box {:class "bg-primary-11 light"}
       [:> Flex {:justify "between" :align "center" :class "px-2 pt-2 pb-1"}
        [:> Text {:as "p" :size "1" :class "px-2 pt-2 pb-1 text-primary-5"} "Selected"]
        [:> IconButton {:size "1"
                        :onClick #(rf/dispatch [:connections/clear-selected])}
         [:> X {:size 14}]]]
       [:> Flex {:align "center" :justify "between" :class "px-2 py-3"}
        [connection-item
         (assoc connection
                :selected? true
                :dark? dark-mode?
                :admin? admin?)]
        [:> Flex {:align "center" :gap "2"}
         [:> IconButton {:onClick #(swap! show-schema? not)
                         :class (if @show-schema? "bg-[--gray-a4]" "")}
          [:> FolderTree {:size 16}]]
         (when admin?
           [:> IconButton
            {:onClick #(rf/dispatch [:navigate :edit-connection {} :connection-name (:name connection)])}
            [:> Settings2 {:size 16}]])]]

       (when (and @show-schema?
                  (= (:type connection) "database")
                  (not= (:access_schema connection) "disabled"))
         [:> Box {:class "bg-[--gray-a4] px-2 py-3"}
          [database-schema/main
           {:connection-name (:name connection)
            :connection-type (cond
                               (not (cs/blank? (:subtype connection))) (:subtype connection)
                               (not (cs/blank? (:icon_name connection))) (:icon_name connection)
                               :else (:type connection))}]])])))

(defn connections-list [connections selected dark-mode? admin?]
  (let [available-connections (if selected
                                (remove #(= (:name %) (:name selected)) connections)
                                connections)]
    [:> Box
     [:> Flex {:justify "between" :align "center" :class "py-3 px-2 bg-gray-1 border-b border-t border-gray-3"}
      [:> Heading {:size "3" :weight "bold" :class "text-gray-12"} "Connections"]
      (when admin?
        [:> Button
         {:size "1"
          :variant "ghost"
          :color "gray"
          :mr "1"
          :onClick #(rf/dispatch [:navigate :create-connection])}
         "Create"])]

     ;; Lista de conexões disponíveis (excluindo a selecionada)
     (for [conn available-connections]
       ^{:key (:name conn)}
       [connection-item
        (assoc conn
               :selected? false
               :dark? dark-mode?
               :admin? admin?
               :on-select #(rf/dispatch [:connections/set-selected conn]))])]))

(defn loading-state []
  [:> Box {:class "flex items-center justify-center h-32"}
   [:> Text {:size "2" :color "gray"} "Loading connections..."]])

(defn error-state [error]
  [:> Box {:class "flex items-center justify-center h-32"}
   [:> Text {:size "2" :color "red"}
    (str "Error loading connections: " error)]])

(defn main []
  (let [status (rf/subscribe [:connections/status])
        connections (rf/subscribe [:connections/filtered])
        selected (rf/subscribe [:connections/selected])
        error (rf/subscribe [:connections/error])
        user (rf/subscribe [:users->current-user])]

    (rf/dispatch [:connections/load-persisted])
    (rf/dispatch [:connections/initialize])

    (fn [dark-mode?]
      (let [admin? (-> @user :data :is_admin)]
        [:> Box {:class "h-full flex flex-col"}
         (case @status
           :loading [loading-state]
           :error [error-state @error]
           :success [:<>
                     (when @selected
                       [selected-connection @selected dark-mode? admin?])
                     [connections-list @connections @selected dark-mode? admin?]]
           [loading-state])]))))

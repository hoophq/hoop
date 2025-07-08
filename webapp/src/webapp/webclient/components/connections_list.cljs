(ns webapp.webclient.components.connections-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading
                               Tooltip IconButton Text Link]]
   ["lucide-react" :refer [AlertCircle EllipsisVertical FolderTree Settings2
                           X ArrowUpRight]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.constants :as connection-constants]
   [webapp.routes :as routes]
   [webapp.webclient.components.alerts-carousel :as alerts-carousel]
   [webapp.webclient.components.database-schema :as database-schema]))

(defn connection-item [{:keys [name command type subtype status selected? on-select dark? admin?]}]
  [:> Box {:class (str "flex justify-between items-center py-3 "
                       "transition "
                       (if selected?
                         "text-gray-1 px-2 bg-primary-11 hover:bg-primary-11 active:bg-primary-12"
                         "text-gray-12 px-4 hover:bg-primary-3 active:bg-primary-4 ")
                       (when @dark? "dark"))
           :onClick (when (not= status "offline") on-select)}
   [:> Flex {:align "center" :gap "2" :justify "between" :class "w-full"}
    [:> Flex {:align "center" :gap "2"}
     [:> Box
      [:figure {:class "w-4"}
       [:img {:src (connection-constants/get-connection-icon
                    {:type type :subtype subtype :command command}
                    "rounded")
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

;; Função memoizada para criar o objeto connection e evitar recriações desnecessárias
(def create-connection-obj
  (memoize
   (fn [connection-name subtype icon_name type]
     {:connection-name connection-name
      :connection-type (cond
                         (not (cs/blank? subtype)) subtype
                         (not (cs/blank? icon_name)) icon_name
                         :else type)})))

(defn selected-connection []
  (let [show-schema? (r/atom true)
        ;; State to avoid premature loading of the heavy component
        schema-loaded? (r/atom true)]
    (fn [connection dark-mode? admin? show-tree?]
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
         (when show-tree?
           [:> Tooltip {:content (if (= (:subtype connection) "cloudwatch")
                                   "Log Groups"
                                   "Database Schema")}
            [:> IconButton {:onClick #(do
                                        (swap! show-schema? not)
                                        ;; Load the schema only when needed
                                        (when (and @show-schema? (not @schema-loaded?))
                                          (reset! schema-loaded? true)))
                            :class (if @show-schema? "bg-[--gray-a4]" "")}
             [:> FolderTree {:size 16}]]])
         (when admin?
           [:> Tooltip {:content "Configure"}
            [:> IconButton
             {:onClick #(rf/dispatch [:navigate :edit-connection {} :connection-name (:name connection)])}
             [:> Settings2 {:size 16}]]])]]

       ;; Tree view of database schema with lazy loading
       (when (and @show-schema?
                  (or (= (:type connection) "database")
                      (= (:subtype connection) "dynamodb")
                      (= (:subtype connection) "cloudwatch"))
                  (not= (:access_schema connection) "disabled"))
         [:> Box {:class "bg-[--gray-a4] px-2 py-3"}
          ;; Lazy loading of the schema component
          (if @schema-loaded?
            [database-schema/main
             (create-connection-obj
              (:name connection)
              (:subtype connection)
              (:icon_name connection)
              (:type connection))]
            ;; Placeholder while we load the real component
            [:div {:class "flex items-center justify-center p-4 text-sm text-gray-400"}
             "Loading database schema..."])])])))

(defn connections-list [connections selected dark-mode? admin?]
  (let [available-connections (if selected
                                (remove #(= (:name %) (:name selected)) connections)
                                connections)
        filtered-connections (filter #(and
                                       (not (#{"tcp" "httpproxy" "ssh"} (:subtype %)))
                                       (or
                                        (= "enabled" (:access_mode_exec %))
                                        (= "enabled" (:access_mode_runbooks %)))) available-connections)]
    [:> Box {:class "h-full pb-4"}
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
     (for [conn filtered-connections]
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

(defn get-active-alerts []
  (let [should-show-license-warning (rf/subscribe [:gateway->should-show-license-expiration-warning])
        hide-setup-local-access (r/atom
                                 (= (js/localStorage.getItem "hide-setup-local-access") "true"))]
    (fn []
      (let [alerts (cond-> []
                     ;; License expiration warning
                     @should-show-license-warning
                     (conj {:id :license-expiration
                            :color "yellow"
                            :icon [:> AlertCircle {:size 16 :class "text-warning-12"}]
                            :text "Your organization's license is expiring soon. Visit the License section to renew it."
                            :action-text "Go to license page"
                            :on-action #(rf/dispatch [:navigate :license-management])
                            :link-href (routes/url-for [:features :license])})

                     (not @hide-setup-local-access)
                     (conj {:id :setup-local-access
                            :color "yellow"
                            :icon [:> AlertCircle {:size 16 :class "text-warning-12"}]
                            :title "Setup Local Access"
                            :text "Enable your local Terminal access for your resources with Hoop CLI."
                            :action-text "Go to Connections"
                            :closeable true
                            :on-close (fn []
                                        (.setItem js/localStorage "hide-setup-local-access" "true")
                                        (reset! hide-setup-local-access true))
                            :on-action #(rf/dispatch [:navigate :connections])
                            :link-href (routes/url-for [:features :license])}))]
        alerts))))

(defn main []
  (let [status (rf/subscribe [:connections/status])
        connections (rf/subscribe [:connections/filtered])
        selected (rf/subscribe [:connections/selected])
        error (rf/subscribe [:connections/error])
        user (rf/subscribe [:users->current-user])]

    ;; Inicializa as conexões e carrega a seleção persistida na ordem correta
    (rf/dispatch [:connections/initialize-with-persistence])

    (fn [dark-mode? show-tree?]
      (let [admin? (-> @user :data :is_admin)]
        [:> Box {:class "h-full flex flex-col"}
         ;; Área principal com scroll para as conexões
         [:> Box {:class "flex-1 overflow-auto"}
          (case @status
            :loading [loading-state]
            :error [error-state @error]
            :success [:<>
                      (when @selected
                        [selected-connection @selected dark-mode? admin? show-tree?])
                      [connections-list @connections @selected dark-mode? admin?]]
            [loading-state])]

         ;; Alerta fixo na parte inferior
         [alerts-carousel/main {:alerts ((get-active-alerts))}]]))))

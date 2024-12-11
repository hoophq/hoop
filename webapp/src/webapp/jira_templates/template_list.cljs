(ns webapp.jira-templates.template-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   ["lucide-react" :refer [ChevronDown ChevronUp Circle]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

;; Mock data
(def mock-connections
  [{:id "1"
    :name "pg-demo"
    :type "PostgreSQL"
    :jira_template "template_1"}
   {:id "2"
    :name "mongodb-test"
    :type "MongoDB"
    :jira_template "template_1"}
   {:id "3"
    :name "oracle-prod"
    :type "Oracle"
    :jira_template "banking+prod_mongodb"}
   {:id "4"
    :name "mysql-dev"
    :type "MySQL"
    :jira_template nil}  ;; Exemplo sem template
   {:id "5"
    :name "redis-cache"
    :type "Redis"
    :jira_template "template_1"}])

(defn- get-template-connections
  [connections template-id]
  ;; (filter #(= (:jira_template %) template-id) connections)
  (filter #(= (:jira_template %) template-id) mock-connections))

(defn- connections-panel [{:keys [name connections]}]
  [:div {:class "px-6 py-4 bg-gray-50 border-t"}
   [:div {:class "mb-2 text-sm font-medium"}
    "Connections List"]
   [:div {:class "text-xs text-gray-500 mb-4"}
    "Check what connections are using this template in their setup."]
   [:div {:class "space-y-1"}
    (for [connection connections]
      ^{:key (:name connection)}
      [:div {:class "flex items-center justify-between py-2"}
       [:div {:class "flex items-center gap-2"}
        [:> Circle {:size 14 :class "text-gray-400"}]
        [:span {:class "text-sm"} (:name connection)]]
       [:> Button {:size "1"
                   :variant "soft"
                   :class "text-xs"}
        "Configure"]])]])

(defn template-item [{:keys [id name description connections on-configure]}]
  (let [show-connections? (r/atom false)]
    (fn []
      [:div {:class (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                         "border-[--gray-a6] data-[disabled]:opacity-70 data-[disabled]:cursor-not-allowed border "
                         (when @show-connections? " bg-gray-50"))}
       [:div {:class "px-6 py-4 flex justify-between items-center"}
        [:div {:class "flex flex-col"}
         [:span {:class "text-sm font-medium"} name]
         [:span {:class "text-sm text-gray-500"} description]]
        [:div {:class "flex items-center gap-2"}
         [:> Button {:size "1"
                     :variant "soft"
                     :color "gray"
                     :class "text-xs"
                     :on-click #(on-configure id)}
          "Configure"]
         [:button {:class "text-xs text-gray-700 hover:text-gray-900 flex items-center gap-1"
                   :on-click #(swap! show-connections? not)}
          "Connections"
          (if @show-connections?
            [:> ChevronUp {:size 14}]
            [:> ChevronDown {:size 14}])]]]
       (when @show-connections?
         [connections-panel {:name name :connections connections}])])))

(defn main [{:keys [templates on-configure]}]
  (let [connections (rf/subscribe [:connections])]
    (rf/dispatch [:connections->get-connections])
    (fn []
      [:div
       (for [template templates]
         ^{:key (:id template)}
         [template-item
          (assoc template
                 :on-configure on-configure
                 :connections (get-template-connections
                              ;;  (:results @connections)
                               []
                               (:name template)))])])))

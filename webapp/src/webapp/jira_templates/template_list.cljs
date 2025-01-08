(ns webapp.jira-templates.template-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.constants :as connection-constants]
   [webapp.connections.views.create-update-connection.main :as create-update-connection]))

(defn- get-template-connections
  [connections template-id]
  (filter #(= (:jira_issue_template_id %) template-id) connections))

(defn- connections-panel [{:keys [connections]}]
  [:> Box {:px "7" :py "5" :class "border-t rounded-b-6 bg-white"}
   [:> Grid {:columns "7" :gap "7"}
    [:> Box {:grid-column "span 2 / span 2"}
     [:> Heading {:as "h4" :size "4" :weight "medium" :class "text-[--gray-12]"}
      "Connections List"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      "Check what connections are using this template in their setup."]]

    [:> Box {:class "h-fit border border-[--gray-a6] rounded-md" :grid-column "span 5 / span 5"}
     (for [connection connections]
       ^{:key (:name connection)}
       [:> Flex {:p "2" :align "center" :justify "between" :class "last:border-b-0 border-b border-[--gray-a6]"}
        [:> Flex {:gap "2" :align "center"}
         [:> Box
          [:figure {:class "w-4"}
           [:img {:src  (connection-constants/get-connection-icon connection)
                  :class "w-9"}]]]
         [:span {:class "text-sm"} (:name connection)]]
        [:> Button {:size "1"
                    :variant "soft"
                    :color "gray"
                    :on-click (fn []
                                (rf/dispatch [:plugins->get-my-plugins])
                                (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                (rf/dispatch [:modal->open {:content [create-update-connection/main :update connection]}]))}
         "Configure"]])]]])

(defn template-item [{:keys [id name description connections on-configure total-items]}]
  (let [show-connections? (r/atom false)]
    (fn []
      [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                           "border-[--gray-a6] border "
                           (when (> total-items 1) " first:border-b-0")
                           (when @show-connections? " bg-[--accent-2]"))}
       [:> Box {:p "5" :class "flex justify-between items-center"}
        [:> Flex {:direction "column"}
         [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
          name]
         [:> Text {:size "3" :class "text-[--gray-11]"} description]]
        [:> Flex {:align "center" :gap "4"}
         [:> Button {:size "3"
                     :variant "soft"
                     :color "gray"
                     :on-click #(on-configure id)}
          "Configure"]
         (when-not (empty? connections)
           [:> Button {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click #(swap! show-connections? not)}
            "Connections"
            (if @show-connections?
              [:> ChevronUp {:size 14}]
              [:> ChevronDown {:size 14}])])]]
       (when @show-connections?
         [connections-panel {:connections connections}])])))

(defn main [{:keys [templates on-configure]}]
  (let [connections (rf/subscribe [:connections])]
    (fn []
      [:> Box
       (doall
        (for [template templates]
          ^{:key (:id template)}
          [template-item
           (assoc template
                  :total-items (count templates)
                  :on-configure on-configure
                  :connections (get-template-connections
                                (:results @connections)
                                (:id template)))]))])))

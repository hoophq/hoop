(ns webapp.agents.panel
  (:require
    [re-frame.core :as rf]
    [webapp.components.headings :as h]
    [webapp.components.button :as button]
    ["lucide-react" :refer [CircleDashed Zap]]
    ["@radix-ui/themes" :refer [Flex Box Text Button
                                Badge Card Link]]))

(defn- agent-item [item]
  (let [connected? (= (:status item) "CONNECTED")]
    [:> Box {:class "p-[--space-5] border-b"}
     [:> Flex {:align "center"}
      [:> Box {:flexGrow "1"}
       [:> Text {:size "4"
                 :weight "bold"
                 :as "div"}
        (:name item)]
       [:> Text {:size "1"
                 :as "div"
                 :class "text-[--gray-11]"}
        (str "Version: " (:version item))]]
      [:> Box
       [:> Badge {:color (if connected? "green" "tomato")}
        [:> CircleDashed {:size 10}]
        (if connected? "Online" "Offline")]]]]))

(defn- empty-state []
  [:> Box {:p "8"}
   [:> Flex {:direction "column" :gap "8"}
    [:> Flex {:direction "column"
              :justify "center"
              :gap "5"
              :align "center"}
     [:> Zap {:size 40
              :style {:color "var(--gray-11)"}}]
     [:> Text {:size "2"
               :color "gray"}
      "Create a new Agent to your Organization to show it here"]
     [:> Button {:size "3"
                 :on-click #(rf/dispatch [:navigate :new-agent])}
      "Setup new Agent"]]
    [:> Flex {:direction "column"
              :justify "center"
              :as "footer"
              :align "center"}
     [:> Text {:size "1"
               :color "gray"}
      "Need more information? Check out our "
      [:> Link {:href "https://hoop.dev/docs/concepts/agent"
                :color "blue"
                :target "_blank"}
       "Agents documentation"]]]]])

(defn- agents-list [agents agents?]
  (if agents?
    [:> Card {:class "p-0"}
     [:> Flex {:direction "column"}
      (for [agent (:data agents)]
        ^{:key (:id agent)}
        [agent-item agent])]]
    [empty-state]))

(defn main []
  (let [agents (rf/subscribe [:agents])
        user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:agents->get-agents])
    (fn []
      (let [agents? (and (seq (:data @agents))
                         (not= (:status @agents) :loading))
            admin? (-> @user :data :is_admin)]
      [:div
       [:> Flex {:class "mb-10", :as "header"}
        [:> Box {:flexGrow "1"}
         [h/PageHeader {:text "Agents"}]
         [:> Text {:size "5" :class "text-[--gray-11]"}
          "View and manage your Agents for your connections"]
           [button/DocsBtnCallOut
            {:text "Learn more about Agents"
             :href "https://hoop.dev/docs/concepts/agent"}]]
        [:> Flex {:justify "end"
                  :flexGrow "1"}
         (when (and agents? admin?)
           [:> Button {:size "3"
                       :on-click #(rf/dispatch [:navigate :new-agent])}
            "Setup new Agent"])]]
       [agents-list @agents agents?]]))))

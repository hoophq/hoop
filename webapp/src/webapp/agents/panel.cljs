(ns webapp.agents.panel
  (:require
    [re-frame.core :as rf]
    [webapp.components.headings :as h]
    [webapp.components.button :as button]
    ["lucide-react" :refer [CircleDashed]]
    ["@radix-ui/themes" :refer [Flex Box Text Button
                                Badge Card]]))

(defn- agent-item [item]
  (let [connected? (= (:status item) "CONNECTED")]
    [:> Box {:class "p-[--space-5] border-b"}
     [:> Flex
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

(defn- agents-list []
  (let [agents (rf/subscribe [:agents])]
    (rf/dispatch [:agents->get-agents])
    (fn []
      [:> Card {:class "p-0"}
       [:> Flex {:direction "column"}
        (for [agent (:data @agents)]
          ^{:key (:id agent)}
          [agent-item agent])
        (for [agent (:data @agents)]
          ^{:key (:id agent)}
          [agent-item agent])]])))

(defn main []
  [:div
   [:> Flex {:class "mb-10", :as "header"}
    [:> Box {:flexGrow "1"}
     [h/H1 {:text "Agents"}]
     [:> Text {:size "5" :class "text-[--gray-11]"}
      "View and manage your Agents for your connections"]
     [button/DocsBtnCallOut
      {:text "Learn more about Agents"
       :href "https://hoop.dev/docs/concepts/agent"}]]
    [:> Flex {:justify "end"
              :flexGrow "1"}
     [:> Button {:size "3"
                 :variant "solid"
                 :on-click #(rf/dispatch [:navigate :new-agent])}
      "Setup new Agent"]]]
   [agents-list]])

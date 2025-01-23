(ns webapp.connections.views.setup.agent-selector
  (:require
   ["@radix-ui/themes" :refer [Box Link Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main []
  (r/create-class
   {:component-did-mount
    (fn [_]
      (rf/dispatch [:agents->get-agents]))

    :reagent-render
    (fn []
      (let [agents @(rf/subscribe [:agents])
            agent-id @(rf/subscribe [:connection-setup/agent-id])]
        [:> Box {:class "space-y-2"}
         [forms/select
          {:label "Agent"
           :placeholder "Select one"
           :required true
           :full-width? true
           :options (mapv #(hash-map :value (:id %)
                                     :text (:name %))
                          (:data agents))
           :selected (or agent-id "")
           :on-change #(rf/dispatch [:connection-setup/set-agent-id %])}]

         [:> Text {:as "p" :size "2" :color "gray"}
          "Agents provide a secure interaction with your connection. "
          [:> Link {:href "https://hoop.dev/docs/concepts/agent"
                    :target "_blank"
                    :size "2"}
           "Read more about Agents."]]]))}))

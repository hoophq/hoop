(ns webapp.agents.new
  (:require
    [re-frame.core :as rf]
    [reagent.core :as r]
    ["@radix-ui/themes" :refer [Grid Flex Box Text
                                Badge Card Avatar Heading]]
    [webapp.config :as config]
    [webapp.components.button :as button]
    [webapp.components.forms :as forms]
    [webapp.components.headings :as h]
    [webapp.agents.deployment :as deployment]))

(defn- installation-method-item [{:keys [icon-dark-path icon-light-path
                                         title description selected?]}]
  [:> Box {:p "2"
           :class (str "border border-[--gray-a6] rounded-xl cursor-pointer"
                       (if selected?
                         " bg-[--accent-12] text-white"
                         " hover:bg-gray-50 transition"))}
   [:> Flex {:gap "3" :align "center"}
    ;; icon
    [:> Box
     [:> Avatar {:size "3"
                 :variant "soft"
                 :color (if selected? "blue" "gray")
                 :fallback (r/as-element
                             [:img {:src (str config/webapp-url
                                              (if selected?
                                                icon-light-path
                                                icon-dark-path))
                                    :alt "Docker"}])}]]
    [:> Box
     [:> Flex {:direction "column"}
      [:> Text {:size "2" :weight "medium"}
       title]
      [:> Text {:size "1" :class (if selected? "text-white" "text-[--gray-11]")}
       description]]]]])

(defn- form []
  (let [installation-methods [{:icon-dark-path "/images/docker-dark.svg"
                               :icon-light-path "/images/docker-light.svg"
                               :title "Docker Hub"
                               :description "Setup a new Agent with a Docker image."}
                              {:icon-dark-path "/images/kubernetes-dark.svg"
                               :icon-light-path "/images/kubernetes-light.svg"
                               :title "Kubernetes"
                               :description "Setup a new Agent with a Helm chart."}]
        selected-installation-method (r/atom "Kubernetes")
        agent-name (r/atom "")]
    (fn []
      [:> Flex {:direction "column" :gap "8"}
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:gridColumn "span 2 / span 2"}
         [:> Heading {:size "4" :weight "medium" :as "h3"}
          "Installation method"]
         [:p {:class "text-sm text-gray-500"}
          "Select the type of environment to setup the agent in your infrastructure."]]
        [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
         [:> Flex {:direction "column" :gap "3"}
          (doall
            (for [method installation-methods]
              [:div {:key (:title method)
                     :on-click #(reset! selected-installation-method
                                        (:title method))}
               [installation-method-item
                (merge method
                       {:selected? (= (:title method)
                                      @selected-installation-method)})]]))]]]
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:gridColumn "span 2 / span 2"}
         [:> Heading {:size "4" :weight "medium" :as "h3"}
          "Set an Agent name"]
         [:p {:class "text-sm text-gray-500"}
          "This name is used to identify the Agent in your environment."]]
        [:> Box {:class "space-y-radix-7" :gridColumn "span 5 / span 5"}
         [forms/input {:label "Name"
                          :placeholder "Enter the name of the Agent"
                          :value @agent-name
                          :on-change #(reset! agent-name (-> % .-target .-value))
                          :required true}]]]
       [deployment/main {:installation-method @selected-installation-method}]])))

(defn main []
  ;; this is dispatched here to avoid refetching when the form receives
  ;; updates from user interaction with it
  ;(rf/dispatch [:agents->generate-agent-key])
  [:div
   [:> Box {:mb "6"}
    [button/HeaderBack]]
   [:> Box {:class "mb-10", :as "header"}
    [h/PageHeader {:text "Setup new Agent"
           :options {:class "mb-2"}}]
    [:> Text {:size "5" :class "text-[--gray-11]" :as "p"}
     "Follow the steps below to setup a new Agent in your environment"]]
   [form]])

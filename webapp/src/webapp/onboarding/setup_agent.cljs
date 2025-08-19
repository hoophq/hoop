(ns webapp.onboarding.setup-agent
  (:require
    [re-frame.core :as rf]
    [reagent.core :as r]
    ["@radix-ui/themes" :refer [Grid Flex Box Text
                                Button Avatar Heading]]
    ["lucide-react" :refer [Info ListOrdered]]
    [webapp.config :as config]
    [webapp.components.button :as button]
    [webapp.components.accordion :as accordion]
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
                                    :alt title}])}]]
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
                               :description "Setup a new Agent with a Helm chart."}
                              {:icon-dark-path "/images/command-line-dark.svg"
                               :icon-light-path "/images/command-line-light.svg"
                               :title "Local or remote machine"
                               :description "Setup a new agent in any machine with hoop CLI"}]
        agent-key (rf/subscribe [:agents->agent-key])
        ;; initial value for the selected installation method
        ;; see webapp/agents/deployment.cljs for more details
        ;; of the multimethod implementation
        selected-installation-method (r/atom "Docker Hub")
        agent-name (r/atom "")

        accordion-agent-information (r/atom true)
        accordion-installation-method (r/atom (= (:status @agent-key) :ready))]
    (fn []
      (r/with-let [_ (add-watch agent-key :agent-key-watcher
                                (fn [_ _ old-val new-val]
                                  (when (and (not= (:status old-val) :ready)
                                             (= (:status new-val) :ready))
                                    (reset! accordion-installation-method true))))]

        [:> Flex {:direction "column" :gap "4"}
         [accordion/root
          {:id "agent-information"
           :open? @accordion-agent-information
           :on-change #(reset! accordion-agent-information %)
           :item {:title "Agent information"
                  :subtitle "Define basic identification properties to create your new Agent."
                  :value "agent-information"
                  :disabled false
                  :avatar-icon [:> Info {:size 16}]
                  :show-icon? (= (:status @agent-key) :ready)
                  :content [:> Grid {:columns "7" :gap "7"}
                            [:> Box {:gridColumn "span 2 / span 2"}
                             [:> Heading {:size "4" :weight "medium" :as "h3"}
                              "Set an Agent name"]
                             [:> Text {:as "p" :size "2" :class "text-[--gray-7]"}
                              "This name is used to identify the Agent in your environment."]]
                            [:> Box {:class "space-y-radix-7" :gridColumn "span 5 / span 5"}
                             [:form {:on-submit #(do
                                                   (js/event.preventDefault)
                                                   (rf/dispatch
                                                     [:agents->generate-agent-key
                                                      @agent-name]))}
                              [forms/input {:label "Name"
                                            :placeholder "Enter the name of the Agent"
                                            :disabled (or
                                                        (= (:status @agent-key) :ready)
                                                        (= (:status @agent-key) :loading))
                                            :value @agent-name
                                            :on-change #(reset! agent-name (-> % .-target .-value))
                                            :required true}]
                              [:> Button {:size "3"
                                          :name "agent-name"
                                          :disabled (or
                                                      (= (:status @agent-key) :ready)
                                                      (= (:status @agent-key) :loading))
                                          :type "submit"
                                          :style {:margin-top "0"}}
                               (if (= (:status @agent-key) :ready)
                                 "Agent created"
                                 "Create Agent")]]]]}}]

         [accordion/root
          {:id "installation-method"
           :open? @accordion-installation-method
           :on-change #(reset! accordion-installation-method %)
           :trigger-value (when (= (:status @agent-key) :ready) "installation-method")
           :item {:title "Installation Method"
                  :value "installation-method"
                  :avatar-icon [:> ListOrdered {:size 16}]
                  :disabled (not (= (:status @agent-key) :ready))
                  :subtitle "Get Agent deployment details for your preferred method."
                  :content [:> Flex {:direction "column" :gap "8"}
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
                            [deployment/main
                             {:installation-method @selected-installation-method
                              :hoop-key (-> @agent-key :data :token)}]
                            [:> Box {:align :right}
                             [:> Button {:size "3"
                                         :on-click #(rf/dispatch [:navigate :onboarding-setup])}
                              "Next"]]]}}]]

        (finally
          (remove-watch agent-key :agent-key-watcher))))))

(defn main []
  ; Reset agent key on mount to avoid cached values
  (rf/dispatch [:agents->set-agent-key nil nil])
  [:> Box {:class "px-32 py-10"}
   [:> Box {:mb "6"}
    [button/HeaderBack]]
   [:> Box {:class "mb-10", :as "header"}
    [h/PageHeader {:text "Setup new Agent"
                   :options {:class "mb-2"}}]
    [:> Text {:size "5" :class "text-[--gray-11]" :as "p"}
     "Follow the steps below to setup a new Agent in your environment"]]
   [form]])

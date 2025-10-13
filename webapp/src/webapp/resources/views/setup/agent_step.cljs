(ns webapp.resources.views.setup.agent-step
  (:require
   ["@radix-ui/themes" :refer [Box Button Dialog Flex Heading Text Avatar]]
   ["lucide-react" :refer [Plus AlertCircle]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.agents.deployment :as deployment]
   [webapp.config :as config]))

(defn agent-not-found-dialog [open? on-close]
  [:> Dialog.Root {:open @open?
                   :onOpenChange #(when-not % (on-close))}
   [:> Dialog.Content {:size "3" :max-width "450px"}
    [:> Flex {:direction "column" :gap "4" :align "center" :class "text-center"}
     [:> Box {:class "flex items-center justify-center w-16 h-16 rounded-full bg-red-3"}
      [:> AlertCircle {:size 32 :class "text-red-9"}]]

     [:> Box
      [:> Dialog.Title {:class "mb-2"}
       [:> Heading {:size "5" :weight "bold"}
        "Agent Not Found"]]
      [:> Dialog.Description
       [:> Text {:size "3" :class "text-gray-11"}
        "The specified agent ID could not be located in the system. Please ensure you have completed the agent configuration process by executing the setup script provided."]]]

     [:> Flex {:gap "3" :class "w-full"}
      [:> Button {:size "3"
                  :variant "soft"
                  :class "flex-1"
                  :on-click on-close}
       "Continue without Agent"]
      [:> Button {:size "3"
                  :class "flex-1"
                  :on-click on-close}
       "Return to Agent Configuration"]]]]])

(defn installation-method-card [{:keys [icon-path title description selected? on-click]}]
  [:> Box {:p "3"
           :on-click on-click
           :class (str "border rounded-lg cursor-pointer transition "
                       (if selected?
                         "border-blue-9 bg-blue-2"
                         "border-gray-6 hover:border-gray-8 hover:bg-gray-2"))}
   [:> Flex {:gap "3" :align "center"}
    [:> Avatar {:size "3"
                :variant "soft"
                :color (if selected? "blue" "gray")
                :fallback (r/as-element
                           [:img {:src (str config/webapp-url icon-path)
                                  :alt title
                                  :class "w-5 h-5"}])}]
    [:> Box
     [:> Text {:size "3" :weight "medium" :class "text-gray-12"}
      title]
     [:> Text {:size "2" :class "text-gray-11"}
      description]]]])

(defn agent-creation-form []
  (let [agent-name (r/atom "")
        installation-method (r/atom "Docker Hub")
        agent-key (rf/subscribe [:agents->agent-key])
        step (r/atom :name)
        dialog-open? (r/atom false)]

    (fn []
      (case @step
        :name
        [:> Box {:class "space-y-6"}
         [:> Box {:class "space-y-3"}
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Set an Agent name"]
          [:> Text {:size "2" :class "text-[--gray-11]"}
           "This name is used to identify your Agent in your environment."]]

         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (rf/dispatch [:agents->generate-agent-key @agent-name])
                              (reset! step :installation))}
          [forms/input {:label "Name"
                        :placeholder "e.g. mycompany-agent"
                        :value @agent-name
                        :required true
                        :on-change #(reset! agent-name (-> % .-target .-value))}]

          [:> Flex {:gap "3" :class "mt-4"}
           [:> Button {:type "button"
                       :variant "soft"
                       :size "3"
                       :on-click #(rf/dispatch [:resource-setup->set-agent-creation-mode :select])}
            "Back to Agent List"]
           [:> Button {:type "submit"
                       :size "3"}
            "Create Agent"]]]]

        :installation
        [:> Box {:class "space-y-6"}
         [:> Box {:class "space-y-3"}
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Installation method"]
          [:> Text {:size "2" :class "text-[--gray-11]"}
           "Select the type of environment to setup the Agent in your infrastructure."]]

         [:> Flex {:direction "column" :gap "3" :class "mb-6"}
          [installation-method-card
           {:icon-path "/images/docker-dark.svg"
            :title "Docker Hub"
            :description "Setup a new Agent with a Docker image."
            :selected? (= @installation-method "Docker Hub")
            :on-click #(reset! installation-method "Docker Hub")}]

          [installation-method-card
           {:icon-path "/images/kubernetes-dark.svg"
            :title "Kubernetes"
            :description "Setup a new Agent with Helm package manager."
            :selected? (= @installation-method "Kubernetes")
            :on-click #(reset! installation-method "Kubernetes")}]]

         (when (= (:status @agent-key) :ready)
           [:> Box {:class "mt-6"}
            [deployment/installation @installation-method (-> @agent-key :data :token)]])

         [:> Flex {:gap "3" :class "mt-6"}
          [:> Button {:variant "soft"
                      :size "3"
                      :on-click #(reset! step :name)}
           "Back"]
          [:> Button {:size "3"
                      :on-click #(do
                                   (reset! dialog-open? true)
                                   (rf/dispatch [:agents->get-agents]))}
           "Agent Created"]]

         [agent-not-found-dialog dialog-open? #(reset! dialog-open? false)]]))))

(defn agent-selector []
  (let [agents (rf/subscribe [:agents])
        agent-id (rf/subscribe [:resource-setup/agent-id])]
    [:> Box {:class "space-y-4"}
     [:> Box {:class "space-y-3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Select an Agent"]
      [:> Text {:size "2" :class "text-[--gray-11]"}
       "This name is used to identify your Agent in your environment."]]

     [forms/select
      {:label "Agent Name"
       :placeholder "Select an Agent"
       :required true
       :full-width? true
       :options (mapv #(hash-map :value (:id %)
                                 :text (:name %))
                      (:data @agents))
       :selected (or @agent-id "")
       :on-change #(when (and % (not= % @agent-id) (not= % ""))
                     (rf/dispatch [:resource-setup->set-agent-id %]))}]

     [:> Button {:variant "ghost"
                 :size "2"
                 :on-click #(rf/dispatch [:resource-setup->set-agent-creation-mode :create])}
      [:> Plus {:size 16}]
      "Create new Agent"]]))

(defn main []
  (let [creation-mode (rf/subscribe [:resource-setup/agent-creation-mode])]
    (r/create-class
     {:component-did-mount
      (fn [_]
        (rf/dispatch [:agents->get-agents]))

      :reagent-render
      (fn []
        [:> Box {:class "max-w-[800px] mx-auto p-8 space-y-8"}
         [:> Box {:class "space-y-4"}
          [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
           "Setup your Organization Agents"]
          [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
           "The Agent serves as the component linking your private infrastructure to Hoop's services. It functions as a proxy, connecting to a central gateway and exposing services within its network scope. Select or create one to get started."]
          [:> Text {:as "p" :size "2" :class "text-blue-9"}
           [:a {:href (get-in config/docs-url [:concepts :agents])
                :target "_blank"}
            "Access our Docs"]
           " to learn more about Agents."]]

         (case (or @creation-mode :select)
           :select [agent-selector]
           :create [agent-creation-form])])})))

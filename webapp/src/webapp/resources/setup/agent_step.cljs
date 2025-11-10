(ns webapp.resources.setup.agent-step
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Dialog Flex Grid Heading Text Link]]
   ["lucide-react" :refer [Plus AlertCircle ArrowUpRight]]
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

(defn installation-method-card [{:keys [icon-path-dark icon-path-light title description selected? on-click]}]
  [:> Card {:size "1"
            :variant "surface"
            :p "3"
            :on-click on-click
            :class (str "w-full cursor-pointer "
                        (when selected? "before:bg-primary-12"))}
   [:> Flex {:align "center" :gap "3" :class (str (when selected? "text-[--gray-1]"))}
    [:> Avatar {:size "4"
                :class (when selected? "dark")
                :variant "soft"
                :color "gray"
                :fallback (r/as-element
                           [:img {:src (str config/webapp-url (if selected?
                                                                icon-path-light
                                                                icon-path-dark))
                                  :alt title
                                  :class "w-5 h-5"}])}]
    [:> Flex {:direction "column"}
     [:> Text {:size "3" :weight "medium"}
      title]
     [:> Text {:size "2"}
      description]]]])

(defn agent-creation-form []
  (let [agent-name (r/atom "")
        installation-method (r/atom "Docker Hub")
        agent-key (rf/subscribe [:agents->agent-key])
        agent-created? (r/atom false)
        dialog-open? (r/atom false)
        agent-id-set? (r/atom false)]

    (r/create-class
     {:component-did-mount
      (fn [_]
        ;; Watch for agent creation success (POST returns token)
        (add-watch agent-key :agent-watcher
                   (fn [_ _ old-val new-val]
                     (when (and (= (:status new-val) :ready)
                                (not= (:status old-val) :ready)
                                (not @agent-id-set?))
                       (js/console.log "🎯 Agent created! Token received")
                       (js/console.log "📦 Agent data:" (clj->js (:data new-val)))
                       ;; Now fetch agents list to get the ID
                       (js/console.log "🔍 Fetching agents list to find agent ID...")
                       (rf/dispatch [:resource-setup->fetch-agent-id-by-name @agent-name])
                       (reset! agent-id-set? true)))))

      :component-will-unmount
      (fn [_]
        ;; Cleanup watcher
        (remove-watch agent-key :agent-watcher))

      :reagent-render
      (fn []
        [:> Box {:class "space-y-16"}
         ;; Agent name section
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 3 / span 3"}
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Set an Agent name"]
           [:> Text {:size "2" :class "text-[--gray-11]"}
            "This name is used to identify your Agent in your environment."]]

          [:> Box {:grid-column "span 4 / span 4"}
           [:form {:on-submit (fn [e]
                                (.preventDefault e)
                                (rf/dispatch [:agents->generate-agent-key @agent-name])
                                (reset! agent-created? true))}
            [forms/input {:label "Name"
                          :placeholder "e.g. mycompany-agent"
                          :value @agent-name
                          :required true
                          :disabled @agent-created?
                          :on-change #(reset! agent-name (-> % .-target .-value))}]

            (when-not @agent-created?
              [:> Button {:type "submit"
                          :size "3"
                          :class "mt-4"}
               "Create Agent"])]]]

         ;; Installation method section - shows after agent is created
         (when @agent-created?
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 3 / span 3"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Installation method"]
             [:> Text {:size "2" :class "text-[--gray-11]"}
              "Select the type of environment to setup the Agent in your service."]]

            [:> Box {:grid-column "span 4 / span 4" :class "space-y-4"}
             [:> Flex {:direction "column" :gap "3"}
              [installation-method-card
               {:icon-path-dark "/images/docker-dark.svg"
                :icon-path-light "/images/docker-light.svg"
                :title "Docker Hub"
                :description "Setup a new Agent with a Docker image."
                :selected? (= @installation-method "Docker Hub")
                :on-click #(reset! installation-method "Docker Hub")}]

              [installation-method-card
               {:icon-path-dark "/images/kubernetes-dark.svg"
                :icon-path-light "/images/kubernetes-light.svg"
                :title "Kubernetes"
                :description "Setup a new Agent with Helm package manager."
                :selected? (= @installation-method "Kubernetes")
                :on-click #(reset! installation-method "Kubernetes")}]]]])

         ;; Deployment instructions - shows when agent key is ready
         (when (= (:status @agent-key) :ready)
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 3 / span 3"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Agent deployment"]
             [:> Text {:size "2" :class "text-[--gray-11]"}
              "Setup your Agent with a docker image or manually run it in your environment."]]

            [:> Box {:grid-column "span 4 / span 4"}
             [deployment/installation @installation-method (-> @agent-key :data :token)]]])

         [agent-not-found-dialog dialog-open? #(reset! dialog-open? false)]])})))

(defn agent-selector [creation-mode]
  (let [agents (rf/subscribe [:agents])
        agent-id (rf/subscribe [:resource-setup/agent-id])
        is-creating? (= creation-mode :create)]
    [:> Grid {:columns "7" :gap "7"}
     [:> Box {:grid-column "span 3 / span 3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Select an Agent"]
      [:> Text {:size "2" :class "text-[--gray-11]"}
       "This name is used to identify your Agent in your environment."]]

     [:> Box {:grid-column "span 4 / span 4" :class "space-y-4"}
      [forms/select
       {:label "Agent Name"
        :placeholder "Select an Agent"
        :required true
        :full-width? true
        :disabled is-creating?
        :options (mapv #(hash-map :value (:id %)
                                  :text (:name %))
                       (:data @agents))
        :selected (or @agent-id "")
        :on-change #(when (and % (not= % @agent-id) (not= % ""))
                      (rf/dispatch [:resource-setup->set-agent-id %]))}]

      (when-not is-creating?
        [:> Button {:variant "ghost"
                    :size "2"
                    :on-click #(rf/dispatch [:resource-setup->set-agent-creation-mode :create])}
         [:> Plus {:size 16}]
         "Create new Agent"])]]))

(defn main []
  (let [creation-mode (rf/subscribe [:resource-setup/agent-creation-mode])]
    (r/create-class
     {:component-did-mount
      (fn [_]
        (rf/dispatch [:agents->get-agents]))

      :reagent-render
      (fn []
        [:> Box {:class "p-8 space-y-16"}
         ;; Header
         [:> Box {:class "space-y-2"}
          [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-gray-12"}
           "Setup your Organization Agents"]
          [:> Text {:as "p" :size "3" :class "text-gray-12"}
           (str "The Agent serves as the component linking your private infrastructure to Hoop's "
                "services. It functions as a proxy, connecting to a central gateway and exposing "
                "services within its network scope. Select or create one to get started.")]
          [:> Text {:as "p" :size "2" :class "text-gray-11 flex items-center gap-1"}
           "Access"
           [:> Flex {:align "center" :gap "1"}
            [:> Link {:href (get-in config/docs-url [:concepts :agents])
                      :target "_blank"}
             " our Docs"]
            [:> ArrowUpRight {:size 12 :class "text-primary-11"}]]
           " to learn more about Agents."]]

         ;; Always show agent selector
         [agent-selector (or @creation-mode :select)]

         ;; Show creation form when in create mode
         (when (= @creation-mode :create)
           [agent-creation-form])])})))

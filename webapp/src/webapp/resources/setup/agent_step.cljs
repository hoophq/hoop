(ns webapp.resources.setup.agent-step
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Grid Heading Text Link Popover TextField]]
   ["lucide-react" :refer [Plus ArrowUpRight Check ChevronDown Search]]
   [clojure.string :as cs]
   [webapp.resources.setup.agent-options :refer [agent-options]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.agents.deployment :as deployment]
   [webapp.config :as config]))

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
  (r/with-let [agent-name (r/atom "")
               installation-method (r/atom "Docker Hub")
               agent-key (rf/subscribe [:agents->agent-key])
               agent-created? (r/atom false)
               agent-id-set? (r/atom false)

               ;; Setup watcher for agent creation success
               _ (add-watch agent-key :agent-watcher
                            (fn [_ _ old-val new-val]
                              (when (and (= (:status new-val) :ready)
                                         (not= (:status old-val) :ready)
                                         (not @agent-id-set?))
                                (rf/dispatch [:resource-setup->fetch-agent-id-by-name @agent-name])
                                (reset! agent-id-set? true)
                                ;; Cleanup watcher after success
                                (remove-watch agent-key :agent-watcher))))]

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
         [deployment/installation @installation-method (-> @agent-key :data :token)]]])]

    (finally
      ;; Cleanup watcher on component unmount
      (remove-watch agent-key :agent-watcher))))

(defn- agent-dropdown
  "Agent select with the search inside its dropdown. Radix Select has no
  search, so this is a Popover with a search field and the option list."
  [_]
  (let [open? (r/atom false)
        search (r/atom "")]
    (fn [{:keys [agents selected-id disabled? on-select]}]
      (let [options (agent-options agents @search)
            selected-name (some #(when (= (:id %) selected-id) (:name %)) agents)
            set-open! (fn [open]
                        (reset! open? open)
                        (when-not open (reset! search "")))
            pick! (fn [value]
                    (on-select value)
                    (set-open! false))]
        [:> Box {:class "w-full"}
         [:> Text {:as "label" :html-for "agent-dropdown-trigger"
                   :size "2" :weight "bold" :class "block mb-1 text-[--gray-12]"}
          "Agent Name"]
         [:> Popover.Root {:open @open? :on-open-change set-open!}
          [:> Popover.Trigger
           [:> Button {:type "button"
                       :id "agent-dropdown-trigger"
                       :variant "surface"
                       :color "gray"
                       :size "3"
                       :disabled disabled?
                       :aria-label "Agent Name"
                       :class "w-full justify-between font-normal"}
            [:> Text {:size "3"
                      :class (str "truncate " (when-not selected-name "text-[--gray-a10]"))}
             (or selected-name "Select an Agent")]
            [:> ChevronDown {:size 16}]]]
          [:> Popover.Content {:size "1"
                               :align "start"
                               :style {:width "var(--radix-popover-trigger-width)"}}
           [:> Box {:mb "2"}
            ;; Uncontrolled: a controlled Radix TextField under Reagent drops
            ;; keys typed fast. The content unmounts on close, so it starts
            ;; empty on every open.
            [:> TextField.Root {:placeholder "Search agents"
                                :aria-label "Search agents"
                                :auto-focus true
                                :default-value ""
                                :on-change #(reset! search (-> % .-target .-value))}
             [:> TextField.Slot
              [:> Search {:size 14}]]]]
           (if (seq options)
             [:> Box {:class "max-h-72 overflow-y-auto" :role "listbox"}
              (doall
               (for [{:keys [value text]} options]
                 ^{:key value}
                 [:> Flex {:align "center"
                           :justify "between"
                           :gap "2"
                           :role "option"
                           :tab-index 0
                           :aria-selected (= value selected-id)
                           :class "cursor-pointer hover:bg-[--gray-a3] focus:bg-[--gray-a3] focus:outline-none rounded px-3 py-2"
                           :on-click #(pick! value)
                           :on-key-down (fn [e]
                                          (when (#{"Enter" " "} (.-key e))
                                            (.preventDefault e)
                                            (pick! value)))}
                  [:> Text {:size "2" :class "truncate"} text]
                  (when (= value selected-id)
                    [:> Check {:size 14}])]))]
             [:> Box {:px "3" :py "4"}
              [:> Text {:size "1" :class "text-[--gray-11] italic"}
               (if (cs/blank? @search)
                 "No agents available"
                 "No agents match the search")]])]]]))))

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
      [agent-dropdown {:agents (:data @agents)
                       :selected-id @agent-id
                       :disabled? is-creating?
                       :on-select #(when (and % (not= % @agent-id))
                                     (rf/dispatch [:resource-setup->set-agent-id %]))}]

      (when-not is-creating?
        [:> Button {:variant "ghost"
                    :size "2"
                    :on-click #(rf/dispatch [:resource-setup->set-agent-creation-mode :create])}
         [:> Plus {:size 16}]
         "Create new Agent"])]]))

(defn main []
  (r/with-let [creation-mode (rf/subscribe [:resource-setup/agent-creation-mode])
               ;; Fetch agents only once on mount
               _ (rf/dispatch [:agents->get-agents])]

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
       [agent-creation-form])]))

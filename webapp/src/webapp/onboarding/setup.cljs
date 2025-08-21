(ns webapp.onboarding.setup
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Heading Spinner Text]]
   ["lucide-react" :refer [DatabaseZap BrainCog PackageSearch PackagePlus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]))

(def setup-options
  [{:id "demo"
    :icon (r/as-element
           [:> DatabaseZap {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "Explore with a demo database"
    :description "Access a preloaded database to it in action."
    :action #(rf/dispatch [:connections->quickstart-create-postgres-demo])}
   {:id "setup"
    :icon (r/as-element
           [:> PackagePlus {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "Setup a connection"
    :description "Add your own services or databases."
    :action #(rf/dispatch [:navigate :onboarding-setup-resource])}
   {:id "aws-connect"
    :icon (r/as-element
           [:> PackageSearch {:size 18 :class "group-hover:text-[--gray-1]"}])
    :title "Automatic resource discovery"
    :badge "BETA"
    :description "Access your resources through your infrastructure providers."
    :action #(rf/dispatch [:navigate :onboarding-resource-providers])}])

(defn setup-card [{:keys [icon title description action badge]}]
  [:> Card {:size "1"
            :variant "surface"
            :class "w-full cursor-pointer hover:before:bg-primary-12 group"
            :on-click action}
   [:> Flex {:align "center" :gap "3" :class "group-hover:text-[--gray-1]"}
    [:> Avatar {:size "4"
                :class "group-hover:bg-[--white-a3]"
                :variant "soft"
                :color "gray"
                :fallback icon}]
    [:> Flex {:direction "column"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
      (when badge
        [:> Box {:class "text-xs font-medium px-2 py-0.5 rounded-full bg-success-9 text-white"}
         badge])]
     [:> Text {:size "2" :color "gray-11"} description]]]])

(defn setup-content []
  [:> Flex {:direction "column" :align "center" :justify "center" :class "h-screen"}
   [:> Box {:class "absolute top-0 right-0 p-radix-5"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]

   [:> Box {:class "spacey-y-radix-7 w-[600px]"}
    [:> Box {:class "space-y-radix-6"}

     [:> Box {:class "spacey-y-radix-7 w-[600px]"}
      [:> Box {:class "space-y-radix-6"}
       [:> Box
        [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
               :class "w-16 mx-auto py-4"}]]

       ;; Title
       [:> Box
        [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
         "How do you want to get started?"]
        [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
         "Choose the setup that works best for you."]]

       ;; Cards
       [:> Box {:class "space-y-radix-4 max-w-[600px]"}
        (for [option setup-options]
          ^{:key (:id option)}
          [setup-card option])]]]]]])

(defn loading-screen []
  [:> Flex {:direction "column" :align "center" :justify "center" :class "h-screen"}
   [:> Box {:class "max-w-[600px] text-center space-y-6"}
    [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
           :class "w-16 mx-auto py-4"}]
    [:> Flex {:align "center" :justify "center"}
     [:> Spinner]]
    [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12] mt-6"}
     "Setting up your environment"]
    [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
     "We're preparing everything you need to get started."]
    [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-4"}
     "This might take a moment as we ensure your agents are ready. While you wait, feel free to learn more about how Agents work in our documentation: "
     [:a {:href "https://hoop.dev/docs/concepts/agents"
          :target "_blank"
          :class "text-blue-500 hover:underline"}
      "https://hoop.dev/docs/concepts/agents"]]]])

(defn setup-agent []
  [:> Flex {:direction "column" :align "center" :justify "center" :class "h-screen"}
   [:> Box {:class "absolute top-0 right-0 p-radix-5"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]

   [:> Box {:class "spacey-y-radix-7 w-[600px]"}
    [:> Box {:class "space-y-radix-6"}

     [:> Box {:class "spacey-y-radix-7 w-[600px]"}
      [:> Box {:class "space-y-radix-6"}
       [:> Box
        [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
               :class "w-16 mx-auto py-4"}]]

       ;; Title
       [:> Box
        [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
         "Setup an Agent"]
        [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
         "Having an agent is essential at hoop."]
        [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
         "Don't worry, you can do it in your local machine to start."]]

       ;; Cards
       [:> Box {:class "space-y-radix-4 max-w-[600px]"}
        [setup-card {:icon (r/as-element
                             [:> BrainCog {:size 18 :class "group-hover:text-[--gray-1]"}])
                     :title "Setup an Agent"
                     :description "Create an agent to start using hoop"
                     :action #(rf/dispatch [:navigate :onboarding-setup-agent])}]]]]]]])

(defn main []
  (let [agents (rf/subscribe [:agents])
        transition-state (r/atom :loading)]
    (rf/dispatch [:agents->get-agents])
    (fn []
      (let [agents-status (:status @agents)
            agents-data (:data @agents)
            agents-available? (and (= agents-status :ready) (seq agents-data))]
        (when (and (= agents-status :ready)
                   (= @transition-state :loading))
          (js/setTimeout
           #(reset! transition-state :ready)
           2000))
        (cond
          ;; If there are no agents available or loading
          (= @transition-state :loading)
          [loading-screen]

          (and (= agents-status :ready)
               (not (seq agents-available?)))
          [setup-agent]

          ;; If the agents are available and the delay has ended
          (and agents-available? (= @transition-state :ready))
          [setup-content])))))

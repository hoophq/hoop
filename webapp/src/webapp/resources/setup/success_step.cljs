(ns webapp.resources.setup.success-step
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Avatar Flex Heading Text]]
   ["lucide-react" :refer [Cable Monitor PackagePlus ShieldCheck Wrench]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.resources.helpers :as helpers]
   [webapp.resources.setup.guardrails-suggestions.views :as guardrails-suggestions]))

(defn action-card [{:keys [icon title description on-click]}]
  [:> Card {:size "2"
            :variant "surface"
            :class "cursor-pointer hover:bg-gray-3 transition-colors"
            :on-click on-click}
   [:> Flex {:gap "4" :align "center"}
    [:> Box {:class "flex items-center justify-center w-10 h-10 rounded-lg bg-green-3"}
     [:> Avatar {:size "4"
                 :variant "soft"
                 :color "gray"
                 :fallback (r/as-element
                            [:> icon {:size 20 :color "gray"}])}]]
    [:> Box {:class "flex-1"}
     [:> Text {:as "div" :size "3" :weight "medium" :class "text-gray-12"}
      title]
     [:> Text {:as "div" :size "2" :class "text-gray-11"}
      description]]]])

(defn main []
  (let [context @(rf/subscribe [:resource-setup/context])
        resource-id @(rf/subscribe [:resource-setup/resource-id])
        created-roles @(rf/subscribe [:resources->last-created-roles])
        processed-roles @(rf/subscribe [:resource-setup/processed-roles])
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        onboarding? (helpers/is-onboarding-context?)
        ;; For add-role context, use created-roles; for setup, use processed-roles (from payload)
        actual-roles (if (= context :add-role) created-roles processed-roles)
        single-role? (= (count actual-roles) 1)
        first-role (first actual-roles)
        ;; Check capabilities of the first role
        can-web-terminal? (helpers/can-open-web-terminal? first-role)
        can-native-client? (helpers/can-access-native-client? first-role)]

    [:> Box {:class "max-w-[840px] mx-auto p-8"}
     ;; Success icon
     [:> Flex {:justify "center" :class "mb-8"}
      [:> Box {:class "flex items-center justify-center w-20 h-20 rounded-full bg-green-3"}
       [:> ShieldCheck {:size 40 :class "text-green-11"}]]]

     ;; Success message
     [:> Box {:class "text-center mb-10"}
      [:> Heading {:as "h2" :size "7" :weight "bold" :class "text-gray-12 mb-3"}
       "Your resource is ready"]
      [:> Text {:as "p" :size "3" :class "text-gray-11"}
       (str "Your " resource-subtype " is now protected by the access gateway.")
       [:br]
       "Every resource role will be authenticated, audited, and secured."]
      ;; Role badges
      (when (seq actual-roles)
        [:> Flex {:justify "center" :gap "2" :wrap "wrap" :class "mt-5"}
         (for [role actual-roles]
           ^{:key (:name role)}
           [:> Badge {:size "2" :variant "solid" :color "indigo" :class "gap-1"}
            [:> Cable {:size 12}]
            (:name role)])])]

     ;; Guardrails suggestions + existing guardrails
     [guardrails-suggestions/main]

     ;; What else you can do
     [:> Box {:class "mb-3"}
      [:> Heading {:as "h3" :size "3" :weight "bold" :class "text-[--gray-12]"}
       "What else you can do"]]
     [:> Box {:class "space-y-3"}
      ;; 1. Configure additional features
      (when single-role?
        [action-card {:icon ShieldCheck
                      :title "Configure additional features"
                      :description "Advanced protections like AI Data Masking, Runbooks and more"
                      :on-click (fn []
                                  (rf/dispatch [:plugins->get-my-plugins])
                                  (rf/dispatch [:navigate :configure-role {} :connection-name (:name first-role)]))}])
      ;; 2. Add another resource
      [action-card {:icon PackagePlus
                    :title "Add another resource"
                    :description "Set up a new resource from scratch"
                    :on-click (fn []
                                (rf/dispatch [:navigate :resource-setup-new]))}]
      ;; 3. Setup Native Access (only single-role + native capable)
      (when (and single-role? can-native-client?)
        [action-card {:icon Wrench
                      :title "Setup Native Access"
                      :description "Connect your IDE or database tools"
                      :on-click (fn []
                                  (rf/dispatch-sync [:navigate :resources])
                                  (rf/dispatch [:native-client-access->start-flow (:name first-role)]))}])
      ;; 4. Test Connection on Web Terminal
      (cond
        (and single-role? can-web-terminal?)
        [action-card {:icon Monitor
                      :title "Test Connection on Web Terminal"
                      :description "Start using your resource immediately in your browser"
                      :on-click (fn []
                                  (js/localStorage.setItem "selected-connection" {:name (:name first-role)})
                                  (rf/dispatch [:database-schema->clear-schema])
                                  (rf/dispatch [:navigate :editor-plugin-panel]))}]

        (not single-role?)
        [action-card {:icon Monitor
                      :title "Test Connection on Web Terminal"
                      :description "Start using your resource immediately in your browser"
                      :on-click (fn []
                                  (rf/dispatch-sync [:primary-connection/clear-selected])
                                  (rf/dispatch [:database-schema->clear-schema])
                                  (rf/dispatch [:navigate :editor-plugin-panel]))}])]

     ;; Footer action
     [:> Flex {:justify "center" :class "mt-8"}
      (if (and onboarding? (not single-role?))
        [:> Button {:size "3"
                    :variant "soft"
                    :on-click #(rf/dispatch [:navigate :home])}
         "Go Home"]
        [:> Button {:size "3"
                    :variant "ghost"
                    :color "gray"
                    :on-click (fn []
                                (if (= context :add-role)
                                  ;; Add-role: go back to resource configure roles tab
                                  (rf/dispatch [:navigate :configure-resource {:tab "roles"} :resource-id resource-id])
                                  ;; Setup: go to connections list
                                  (rf/dispatch [:navigate :resources])))}
         "Skip and configure later"])]]))

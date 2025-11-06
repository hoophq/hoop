(ns webapp.resources.views.setup.success-step
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Avatar Flex Heading Text]]
   ["lucide-react" :refer [ChevronRight Monitor ShieldCheck Wrench]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.resources.helpers :as helpers]))

(defn action-card [{:keys [icon title description recommended? on-click]}]
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
     [:> Flex {:gap "2" :align "center" :class "mb-1"}
      [:> Text {:size "3" :weight "medium" :class "text-gray-12"}
       title]
      (when recommended?
        [:> Badge {:size "1" :variant "soft" :color "blue"}
         "Recommended"])]
     [:> Text {:size "2" :class "text-gray-11"}
      description]]
    [:> Box
     [:> ChevronRight {:size 20 :class "text-gray-9"}]]]])

(defn main []
  (let [context @(rf/subscribe [:resource-setup/context])
        resource-id @(rf/subscribe [:resource-setup/resource-id])
        resource @(rf/subscribe [:resources->last-created])
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

    [:> Box {:class "max-w-[640px] mx-auto p-8"}
     ;; Success icon
     [:> Flex {:justify "center" :class "mb-8"}
      [:> Box {:class "flex items-center justify-center w-20 h-20 rounded-full bg-green-3"}
       [:> ShieldCheck {:size 40 :class "text-green-11"}]]]

     ;; Success message
     [:> Box {:class "text-center mb-10"}
      [:> Heading {:as "h2" :size "7" :weight "bold" :class "text-gray-12 mb-3"}
       (if single-role?
         (str (:name resource "Your resource") " is ready")
         "Your resource is ready")]
      [:> Text {:as "p" :size "3" :class "text-gray-11"}
       (str "Your " resource-subtype " is now protected by the access gateway. "
            "Every connection will be authenticated, audited, and secured.")]]

     ;; Next steps
     [:> Box {:class "space-y-3"}
      (if single-role?
        ;; Single role: Show options based on capabilities
        [:<>
         ;; Test Connection - only if web terminal is available
         (when can-web-terminal?
           [action-card {:icon Monitor
                         :title "Test Connection on Web Terminal"
                         :description "Start using your resource immediately in your browser"
                         :recommended? true
                         :on-click (fn []
                                     (js/localStorage.setItem "selected-connection" {:name (:name first-role)})
                                     (rf/dispatch [:database-schema->clear-schema])
                                     (rf/dispatch [:navigate :editor-plugin-panel]))}])

         ;; Setup Native Access - only if native client is available
         (when can-native-client?
           [action-card {:icon Wrench
                         :title "Setup Native Access"
                         :description "Connect your IDE or database tools"
                         :on-click #(js/console.log "Setup native access clicked")}])

         ;; Configure Additional Features - always show
         [action-card {:icon ShieldCheck
                       :title "Configure Additional Features"
                       :description "Advanced protection like AI Data Masking, Runbooks and more"
                       :on-click (fn []
                                   (rf/dispatch [:plugins->get-my-plugins])
                                   (rf/dispatch [:navigate :configure-role {} :connection-name (:name first-role)]))}]]

        ;; Multiple roles: Show only 2 options
        [:<>
         [action-card {:icon Monitor
                       :title "Test Connection on Web Terminal"
                       :description "Start using your resource immediately in your browser"
                       :recommended? true
                       :on-click (fn []
                                   (rf/dispatch-sync [:primary-connection/clear-selected])
                                   (rf/dispatch [:database-schema->clear-schema])
                                   (rf/dispatch [:navigate :editor-plugin-panel]))}]

         [action-card {:icon ChevronRight
                       :title "Go to Resources"
                       :description "Access your configured resources and roles"
                       :on-click (fn []
                                   (if (= context :add-role)
                                     ;; Add-role: go back to resource configure roles tab
                                     (rf/dispatch [:navigate :configure-resource {:tab "roles"} :resource-id resource-id])
                                     ;; Setup: go to connections list
                                     (rf/dispatch [:navigate :connections])))}]])]

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
                                  (rf/dispatch [:navigate :connections])))}
         "Skip and configure later"])]]))

(ns webapp.resources.setup.success-step
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Avatar Flex Heading Text]]
   ["lucide-react" :refer [Cable ChevronRight Monitor PackagePlus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.resources.helpers :as helpers]
   [webapp.features.activation-journey.views.feature-cards :as feature-cards]))

(defn action-card [{:keys [icon title description badge on-click]}]
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
     [:> Flex {:gap "2" :align "center"}
      [:> Text {:as "div" :size "3" :weight "medium" :class "text-gray-12"}
       title]
      (when badge
        [:> Badge {:size "1" :variant "soft" :color "indigo"}
         badge])]
     [:> Text {:as "div" :size "2" :class "text-gray-11"}
      description]]
    [:> ChevronRight {:size 16 :class "shrink-0 text-gray-11" :aria-hidden true}]]])

(defn main []
  (let [context @(rf/subscribe [:resource-setup/context])
        created-roles @(rf/subscribe [:resources->last-created-roles])
        processed-roles @(rf/subscribe [:resource-setup/processed-roles])
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        onboarding? (helpers/is-onboarding-context?)
        ;; For add-role context, use created-roles; for setup, use processed-roles (from payload)
        actual-roles (if (= context :add-role) created-roles processed-roles)
        single-role? (= (count actual-roles) 1)
        first-role (first actual-roles)
        ;; Check capabilities of the first role
        can-web-terminal? (helpers/can-open-web-terminal? first-role)]

    [:> Box {:class "px-[98px] py-10"}
     ;; Success icon
     [:> Flex {:justify "center" :class "mb-8"}
      [:> Box {:class "flex items-center justify-center w-20 h-20 rounded-full bg-green-3"}
       [:img {:src "/icons/icon-lock-big.svg"
              :class "w-[150px] h-[150px]"
              :alt "Lock icon, success."}]]]

     ;; Success message
     [:> Box {:class "text-center mb-12"}
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

     ;; Next steps
     [:> Box {:class "mb-4"}
      [:> Heading {:as "h3" :size "3" :weight "bold" :class "text-[--gray-12]"}
       "Next steps:"]]
     [:> Box {:class "space-y-3 mb-12"}
      ;; 1. Test Connection on Web Terminal
      (cond
        (and single-role? can-web-terminal?)
        [action-card {:icon Monitor
                      :title "Test Connection on Web Terminal"
                      :badge "Recommended"
                      :description "Start using your resource immediately in your browser"
                      :on-click (fn []
                                  (rf/dispatch-sync [:database-schema->clear-schema])
                                  (rf/dispatch [:navigate :editor-plugin {:role (:name first-role)}]))}]

        (not single-role?)
        [action-card {:icon Monitor
                      :title "Test Connection on Web Terminal"
                      :badge "Recommended"
                      :description "Start using your resource immediately in your browser"
                      :on-click (fn []
                                  (rf/dispatch-sync [:primary-connection/clear-selected])
                                  (rf/dispatch-sync [:database-schema->clear-schema])
                                  (rf/dispatch [:navigate :editor-plugin]))}])
      ;; 2. Add another resource
      [action-card {:icon PackagePlus
                    :title "Add another resource"
                    :description "Set up a new resource from scratch"
                    :on-click (fn []
                                (rf/dispatch [:navigate :resource-catalog]))}]]

     ;; Activation journey feature cards
     [feature-cards/main {:subtype resource-subtype
                          :surface :resource-success
                          :with-roles? true}]

     ;; Footer action — only the onboarding "Go Home" remains
     (when (and onboarding? (not single-role?))
       [:> Flex {:justify "center" :class "mt-10"}
        [:> Button {:size "3"
                    :variant "soft"
                    :on-click #(rf/dispatch [:navigate :home])}
         "Go Home"]])]))

(ns webapp.resources.setup.guardrails-suggestions.views
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Checkbox Flex
                               Heading Switch Text]]
   ["lucide-react" :refer [Check ChevronDown ChevronRight]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.features.promotion :as promotion]))

(defn- upgrade-button []
  [:> Button {:size "2"
              :variant "soft"
              :color "indigo"
              :on-click (fn [e]
                          (.stopPropagation e)
                          (promotion/request-demo))}
   "Upgrade"])

(defn- check-pill []
  [:> Avatar {:size "1"
              :variant "soft"
              :color "green"
              :radius "full"
              :fallback (r/as-element [:> Check {:size 14}])}])

(defn suggestion-card
  "Renders one suggestion row with checkbox + accordion of role toggles."
  [suggestion roles disabled-by-plan?]
  (let [sname (:name suggestion)
        {:keys [checked? selected-roles pending?]}
        @(rf/subscribe [:guardrails-suggestions/suggestion-state sname])
        all-conn-ids (mapv :id roles)
        disabled? (or pending?
                      (and disabled-by-plan? (not checked?)))]
    [:> (.-Item Accordion)
     {:value sname
      :className (str "border-b last:border-b-0 border-[--gray-a4] "
                      "data-[state=open]:bg-[--accent-2]")}
     [:> Flex {:align "center" :gap "3" :class "px-4 py-3 w-full"}
      [:> Checkbox
       {:checked checked?
        :disabled disabled?
        :onCheckedChange
        (fn [_]
          (rf/dispatch [:guardrails-suggestions/toggle-checkbox
                        suggestion all-conn-ids]))}]
      [:> Box {:class "flex-1"}
       [:> Text {:as "div" :size "3" :weight "bold" :class "text-[--gray-12]"}
        (:title suggestion)]
       [:> Text {:as "div" :size "2" :class "text-[--gray-11]"}
        (:card-description suggestion)]]
      (cond
        (and disabled-by-plan? (not checked?))
        [upgrade-button]

        checked?
        [:> Flex {:align "center" :gap "2"}
         [check-pill]
         [:> (.-Trigger Accordion) {:asChild true}
          [:> Button {:size "1" :variant "ghost" :color "gray"
                      :class "group p-1"}
           [:> ChevronDown {:size 18
                            :className "transition-transform duration-200 group-data-[state=open]:rotate-180"}]]]]

        :else
        [:> (.-Trigger Accordion) {:asChild true}
         [:> Button {:size "1" :variant "ghost" :color "gray"
                     :class "group p-1"
                     :disabled disabled?}
          [:> ChevronDown {:size 18
                           :className "transition-transform duration-200 group-data-[state=open]:rotate-180"}]]])]
         [:> (.-Content Accordion)
          [:> Box {:class "px-4 pb-4 border-t border-[--gray-a4]"}
           [:> Box {:class "pt-4 pb-2"}
            [:> Text {:as "div" :size "2" :weight "bold" :class "text-[--gray-12]"}
             "Configure roles"]
            [:> Text {:as "div" :size "1" :class "text-[--gray-11]"}
             "Select which roles will receive this Guardrails rule"]]
           [:> Flex {:direction "column" :gap "3" :class "pt-2"}
            (for [role roles
                  :let [conn-id (:id role)
                        on? (contains? selected-roles conn-id)]]
              ^{:key conn-id}
              [:> Flex {:align "center" :gap "3"}
               [:> Switch
                {:checked on?
                 :size "2"
                 :disabled (or pending?
                               (and disabled-by-plan? (not checked?)))
                 :onCheckedChange
                 (fn [next-on?]
                   (rf/dispatch [:guardrails-suggestions/toggle-role
                                 suggestion conn-id next-on?]))}]
               [:> Badge {:size "2" :variant "soft" :color "indigo"}
                (:name role)]])]]]]))

(defn- your-guardrail-card
  "Read-only card for an existing guardrail. Free-plan users see Upgrade
  in place of the chevron; paid users get a chevron that navigates to
  the edit screen."
  [guardrail free?]
  [:> Card {:size "2"
            :variant "surface"
            :class (str "cursor-pointer hover:bg-gray-3 transition-colors "
                        (when free? "opacity-90"))
            :on-click (when-not free?
                        (fn []
                          (rf/dispatch [:navigate :edit-guardrail {}
                                        :guardrail-id (:id guardrail)])))}
   [:> Flex {:gap "3" :align "center"}
    [:> Checkbox {:checked true :disabled true}]
    [:> Box {:class "flex-1"}
     [:> Text {:as "div" :size "3" :weight "bold" :class "text-[--gray-12]"}
      (:name guardrail)]
     (when (seq (:description guardrail))
       [:> Text {:as "div" :size "2" :class "text-[--gray-11]"}
        (:description guardrail)])]
    (if free?
      [upgrade-button]
      [:> ChevronRight {:size 18 :class "text-[--gray-9]"}])]])

(defn main []
  (rf/dispatch [:guardrails-suggestions/init])
  (fn []
    (let [suggestions @(rf/subscribe [:guardrails-suggestions/list-for-subtype])
          roles @(rf/subscribe [:guardrails-suggestions/roles-with-ids])
          top-3 @(rf/subscribe [:guardrails-suggestions/top-3-other])
          free? @(rf/subscribe [:guardrails-suggestions/free-license?])
          limit-reached? @(rf/subscribe [:guardrails-suggestions/limit-reached?])
          disabled-by-plan? (and free? limit-reached?)]
      (when (or (seq suggestions) (seq top-3))
        [:> Box {:class "space-y-6 mb-8"}
         (when (seq suggestions)
           [:> Box
            [:> Heading {:as "h3" :size "3" :weight "bold"
                         :class "text-[--gray-12] mb-3"}
             "Protect your resource with Guardrails"]
            [:> Card {:size "1" :class "p-0 overflow-hidden"}
             [:> (.-Root Accordion)
              {:type "single" :collapsible true :className "w-full"}
              (for [s suggestions]
                ^{:key (:name s)}
                [suggestion-card s roles disabled-by-plan?])]]])
         (when (seq top-3)
           [:> Box
            [:> Heading {:as "h3" :size "3" :weight "bold"
                         :class "text-[--gray-12] mb-3"}
             "Your Guardrails"]
            [:> Box {:class "space-y-2"}
             (for [g top-3]
               ^{:key (:id g)}
               [your-guardrail-card g free?])]])]))))

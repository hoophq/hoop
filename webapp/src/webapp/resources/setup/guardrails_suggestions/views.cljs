(ns webapp.resources.setup.guardrails-suggestions.views
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Checkbox Flex
                               Heading Switch Text]]
   ["lucide-react" :refer [Cable Check ChevronRight]]
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
      :disabled (and disabled-by-plan? (not checked?))
      :className (str "group/item border-b last:border-b-0 border-[--gray-a4] "
                      "data-[disabled]:opacity-90")}
     [:> Flex {:align "center" :gap "3"
               :class (str "px-5 py-4 w-full "
                           "group-data-[state=open]/item:bg-[--accent-2]")}
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
        [:> Flex {:align "center" :gap "4"}
         [check-pill]
         [:> (.-Trigger Accordion) {:asChild true}
          [:> Button {:size "1" :variant "ghost" :color "gray"
                      :class "group p-1"}
           [:> ChevronRight {:size 18
                             :className "transition-transform duration-200 group-data-[state=open]:rotate-90"}]]]]

        :else
        [:> (.-Trigger Accordion) {:asChild true}
         [:> Button {:size "1" :variant "ghost" :color "gray"
                     :class "group p-1"
                     :disabled disabled?}
          [:> ChevronRight {:size 18
                            :className "transition-transform duration-200 group-data-[state=open]:rotate-90"}]]])]
     [:> (.-Content Accordion)
      [:> Box {:class "px-7 py-7 border-t border-[--gray-a4] bg-white"}
       [:> Flex {:justify "between" :align "start" :gap "6"}
        [:> Box {:class "flex-1"}
         [:> Text {:as "div" :size "3" :weight "bold" :class "text-[--gray-12]"}
          "Configure roles"]
         [:> Text {:as "div" :size "2" :class "text-[--gray-11]"}
          "Select which roles will receive this Guardrails rule"]]
        [:> Flex {:direction "column" :gap "3" :class "shrink-0"}
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
            [:> Badge {:size "2" :variant "soft" :color "indigo"
                       :class "gap-1"}
             [:> Cable {:size 12}]
             (:name role)]])]]]]]))

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
              {:type "multiple" :className "w-full"}
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

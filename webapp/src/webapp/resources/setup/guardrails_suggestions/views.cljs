(ns webapp.resources.setup.guardrails-suggestions.views
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Checkbox Flex
                               Heading Switch Text]]
   ["lucide-react" :refer [Cable Check ChevronRight]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.accordion :as accordion]
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

(defn- stop-propagation
  "Wraps a node so that mouse and keyboard events do not bubble up to the
  Accordion.Trigger that wraps the entire row."
  [node]
  [:div {:on-click #(.stopPropagation %)
         :on-key-down #(.stopPropagation %)
         :on-pointer-down #(.stopPropagation %)}
   node])

(defn- suggestion-content [suggestion roles selected-roles checked? pending? disabled-by-plan?]
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
       [:> Badge {:size "2" :variant "soft" :color "indigo" :class "gap-1"}
        [:> Cable {:size 12}]
        (:name role)]])]])

(defn suggestion-card
  "Renders one suggestion row using the shared accordion component."
  [suggestion roles disabled-by-plan?]
  (let [sname (:name suggestion)
        {:keys [checked? selected-roles pending?]}
        @(rf/subscribe [:guardrails-suggestions/suggestion-state sname])
        all-conn-ids (mapv :id roles)
        plan-blocked? (and disabled-by-plan? (not checked?))
        checkbox-disabled? (or pending? plan-blocked?)
        left [stop-propagation
              [:> Checkbox
               {:checked checked?
                :disabled checkbox-disabled?
                :onCheckedChange
                (fn [_]
                  (rf/dispatch [:guardrails-suggestions/toggle-checkbox
                                suggestion all-conn-ids]))}]]
        right (cond
                plan-blocked?
                [stop-propagation [upgrade-button]]

                checked?
                [:div {:className "flex space-x-3 items-center"}
                 [check-pill]
                 [accordion/chevron-icon]]

                :else nil)]
    [accordion/accordion-item
     {:value sname
      :disabled plan-blocked?
      :title (:title suggestion)
      :subtitle (:card-description suggestion)
      :title-size "3"
      :subtitle-size "2"
      :title-weight "bold"
      :trigger-padding "px-5 py-4"
      :item-class (str "border-b last:border-b-0 border-[--gray-a4] "
                       "data-[state=open]:bg-[--accent-2] "
                       "data-[disabled]:opacity-90")
      :content-class "bg-white border-t border-[--gray-a4] px-7 py-7"
      :left-slot left
      :right-slot right
      :content [suggestion-content suggestion roles selected-roles
                checked? pending? disabled-by-plan?]}]))

(defn- your-guardrail-card
  "Read-only card for an existing guardrail."
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
              {:type "multiple"
               :className "w-full"
               :value (clj->js @(rf/subscribe [:guardrails-suggestions/open-items]))
               :onValueChange #(rf/dispatch [:guardrails-suggestions/set-open-items
                                             (js->clj %)])}
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

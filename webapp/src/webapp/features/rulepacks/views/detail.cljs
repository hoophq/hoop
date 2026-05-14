(ns webapp.features.rulepacks.views.detail
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Tabs Text]]
   ["lucide-react" :refer [ArrowLeft]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.features.rulepacks.views.configuration-tab :as configuration-tab]
   [webapp.features.rulepacks.views.roles-tab :as roles-tab]))

(defn- header [{:keys [display_name description]}]
  [:> Box {:pb "5" :pt "7" :class "w-full"}
   [:> Flex {:direction "column" :gap "4" :class "w-full"}
    [:> Flex {:gap "1" :align "center"
              :class "cursor-pointer text-[--gray-11] hover:text-[--gray-12]"
              :on-click #(rf/dispatch [:navigate :rulepacks])}
     [:> ArrowLeft {:size 16}]
     [:> Text {:size "3"} "Rulepacks"]]
    [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-[--gray-12]"}
     (or display_name "")]
    (when (seq description)
      [:> Text {:size "5" :class "text-[--gray-11]"}
       description])]])

(defn main [{:keys [rulepack-id]}]
  (rf/dispatch [:rulepacks/get rulepack-id])
  (fn [_]
    (let [rulepack @(rf/subscribe [:rulepacks/active])
          status @(rf/subscribe [:rulepacks/active-status])
          selected @(rf/subscribe [:rulepacks/selected-connections])
          loading? (or (= :loading status) (nil? rulepack))
          rule-count (+ (count (or (:data_masking_rules rulepack) []))
                        (count (or (:guardrail_rules rulepack) [])))
          selected-count (count selected)]
      [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
       (if loading?
         [:> Flex {:justify "center" :align "center" :py "9"}
          [loaders/simple-loader]]
         [:<>
          [header rulepack]
          [:> Tabs.Root {:default-value "roles" :class "w-full"}
           [:> Tabs.List
            [:> Tabs.Trigger {:value "roles"}
             (str "Roles • " selected-count " selected")]
            [:> Tabs.Trigger {:value "configuration"}
             (str "Configuration • " rule-count " rule" (when (not= 1 rule-count) "s"))]]
           [:> Box {:pt "4" :class "w-full"}
            [:> Tabs.Content {:value "roles" :class "w-full"}
             [roles-tab/main]]
            [:> Tabs.Content {:value "configuration" :class "w-full"}
             [configuration-tab/main {:rulepack rulepack}]]]]])])))

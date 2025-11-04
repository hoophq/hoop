(ns webapp.resources.views.configure-role.native-access-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]))

(defn toggle-section
  [{:keys [title
           description
           checked
           disabled?
           on-change
           upgrade-plan-component
           learning-component]}]
  [:> Flex {:align "center" :gap "5"}
   [:> Switch {:checked checked
               :size "3"
               :disabled disabled?
               :onCheckedChange on-change}]
   [:> Box
    [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"} title]
    [:> Text {:as "p" :size "2" :class "text-[--gray-11]"} description]

    (when upgrade-plan-component
      upgrade-plan-component)

    (when learning-component
      learning-component)]])

(defn main [_connection]
  (let [user (rf/subscribe [:users->current-user])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        review? (rf/subscribe [:connection-setup/review])]
    
    (fn [_connection]
      (let [free-license? (-> @user :data :free-license?)
            native-access-enabled? (get @access-modes :native true)]
        
        [:> Box {:class "max-w-[600px] space-y-8"}
         
         ;; Native access availability
         [toggle-section
          {:title "Native access availability"
           :description "Access from your client of preference using hoop.dev to channel connections using our Desktop App or our Command Line Interface."
           :checked native-access-enabled?
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :native])}]
         
         ;; Just-in-Time Review
         [toggle-section
          {:title "Just-in-Time Review"
           :description "Require approval prior to connection execution."
           :checked @review?
           :on-change #(rf/dispatch [:connection-setup/toggle-review])
           :learning-component
           [:> Button {:variant "ghost" :size "2" :class "mt-3"}
            [:> ArrowUpRight {:size 16}]
            "Learn more about Reviews"]}]
         
         ;; AI Data Masking
         [toggle-section
          {:title "AI Data Masking"
           :description "Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
           :checked false
           :disabled? true
           :upgrade-plan-component
           (when free-license?
             [:> Callout.Root {:size "1" :class "mt-4" :color "blue"}
              [:> Callout.Icon [:> Star {:size 16}]]
              [:> Callout.Text
               "Enable AI Data Masking by "
               [:> Link {:onClick #(rf/dispatch [:navigate :upgrade-plan])}
                "upgrading your plan."]]])
           :learning-component
           [:> Button {:variant "ghost" :size "2" :class "mt-3"}
            [:> ArrowUpRight {:size 16}]
            "Learn more about AI Data Masking"]}]]))))


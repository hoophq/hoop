(ns webapp.integrations.jira.main
  (:require ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Link Switch Text]]
            ["lucide-react" :refer [Info]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.forms :as forms]
            [webapp.components.loaders :as loaders]))

(defn convert-status-to-boolean [status]
  (if (= status "enabled") true false))

(defn- configurations-view [integration-details]
  (let [is-create-or-update (if (empty? (-> integration-details :data)) :create :update)
        jira-enabled? (r/atom (or (convert-status-to-boolean
                                   (-> integration-details :data :status)) false))
        api-token (r/atom (or (-> integration-details :data :api_token) ""))
        url (r/atom (or (-> integration-details :data :url) ""))
        user-email (r/atom (or (-> integration-details :data :user) ""))
        project-key (r/atom (or (-> integration-details :data :project_key) ""))]
    (fn []
      [:> Box {:p "5" :class "bg-white rounded-md border border-gray-100"}
       [:> Grid {:columns "3" :gap "7"}
        [:> Box {:grid-column "span 1 /span 1"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-gray-12"}
          "Configure integration"]
         [:> Text {:as "p" :size "3" :class "text-gray-11"}
          "Boost productivity by linking your resource roles with Jira."]]
        [:> Box {:grid-column "span 2 /span 2"}
         [:form
          {:on-submit (fn [e]
                        (let [payload {:url @url
                                       :user @user-email
                                       :api_token @api-token
                                       :status (if @jira-enabled? "enabled" "disabled")}]

                          (.preventDefault e)
                          (if (= :create is-create-or-update)
                            (rf/dispatch [:jira-integration->create payload])
                            (rf/dispatch [:jira-integration->update payload]))))}
          [:> Box {:class "space-y-radix-7"}
           [:> Flex {:align "center" :gap "3"}
            [:> Switch {:checked @jira-enabled?
                        :size "3"
                        :onCheckedChange #(reset! jira-enabled? %)}]
            [:> Text {:size "3" :weight "medium" :class "text-gray-12"}  "Enable integration"]]
           [forms/input {:label "Jira Instance URL"
                         :placeholder "https://your-domain.atlassian.net"
                         :on-change #(reset! url (-> % .-target .-value))
                         :classes "whitespace-pre overflow-x"
                         :disabled (not @jira-enabled?)
                         :required true
                         :not-margin-bottom? true
                         :value @url}]
           [forms/input {:label "User Email"
                         :placeholder "name@company.com"
                         :on-change #(reset! user-email (-> % .-target .-value))
                         :classes "whitespace-pre overflow-x"
                         :disabled (not @jira-enabled?)
                         :required true
                         :not-margin-bottom? true
                         :value @user-email}]
           [:> Box {:class "space-y-radix-2 first:mb-0"}
            [forms/textarea {:label "User API token"
                             :placeholder "lXtpBPQvBvSycVYDGo7S8k2N12KE1dMcdyastG"
                             :on-change #(reset! api-token (-> % .-target .-value))
                             :classes "whitespace-pre overflow-x"
                             :type "password"
                             :disabled (not @jira-enabled?)
                             :required true
                             :not-margin-bottom? true
                             :value @api-token}]
            [:> Flex {:gap "2" :align "center"}
             [:> Info {:size 16 :class "text-gray-11"}]
             [:> Text {:size "2" :class "text-gray-11"}
              "For more information about how to find your User API token, "
              [:> Link {:href "https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/"
                        :target "_blank"}
               "click here."]]]]]
          [:> Box {:mt "8"}
           [:> Button {:variant "primary"
                       :type "submit"}
            "Confirm"]]]]]])))

(defn main []
  (let [integration-details (rf/subscribe [:jira-integration->details])]
    (rf/dispatch [:jira-integration->get])
    (fn []
      [:div
       (if (-> @integration-details :loading)
         [:> Box {:p "5" :minHeight "800px" :class "bg-white rounded-md border border-gray-100 overflow-y-auto"}
          [:> Flex {:justify "center" :align "center" :gap "3"}
           [loaders/simple-loader]]]
         [configurations-view @integration-details])])))

